package websearchconnect

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// Routes serves the three routes. All fields are required.
type Routes struct {
	Rows     Rows
	Verifier Verifier
	// Seal seals a key before it is stored: sealedsecret under the web
	// search purpose, the one the chat's tools open it with.
	Seal   sealedsecret.Seal
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (r Routes) Check() error {
	if r.Rows == nil || r.Verifier == nil || !r.Seal.Ready() || r.Logger == nil {
		return errors.New("websearchconnect: the rows, the verifier, the seal and the logger are all required")
	}
	return nil
}

// What the routes say.
const (
	sessionRequired = "This action requires an interactive session"
	unknownProvider = "Web search providers are exa and tavily"
	notConnected    = "This provider is not connected"
	keyRequired     = "Enter the API key"
	keyMalformed    = "An API key is one line of letters, digits and punctuation, without spaces"
)

// mostKeyBytes is the longest key accepted: far beyond either provider's.
const mostKeyBytes = 512

// Register adds the three operations.
func (r Routes) Register(api huma.API) {
	stream := func(serve func(huma.Context)) *huma.StreamResponse { return &huma.StreamResponse{Body: serve} }
	type named struct {
		Provider string `path:"provider" doc:"exa or tavily"`
	}
	huma.Register(api, huma.Operation{
		OperationID: "web-search-connections", Method: http.MethodGet, Path: ListPath,
		Summary:     "The account's web search connections",
		Description: "Exa and Tavily, each connected or not, with the stored key's mask. Never the key.",
		Tags:        []string{"connectors"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.list), nil })
	huma.Register(api, huma.Operation{
		OperationID: "connect-web-search", Method: http.MethodPut, Path: ProviderPath,
		Summary:     "Connect Exa or Tavily",
		Description: "Checks the key with the provider, then stores it sealed. A session only.",
		Tags:        []string{"connectors"},
	}, func(context.Context, *named) (*huma.StreamResponse, error) { return stream(r.connect), nil })
	huma.Register(api, huma.Operation{
		OperationID: "disconnect-web-search", Method: http.MethodDelete, Path: ProviderPath,
		Summary:     "Disconnect Exa or Tavily",
		Description: "Removes the stored key. A session only.", Tags: []string{"connectors"},
	}, func(context.Context, *named) (*huma.StreamResponse, error) { return stream(r.disconnect), nil })
}

// shown is one provider as the screen reads it.
type shown struct {
	Provider    string  `json:"provider"`
	Connected   bool    `json:"connected"`
	KeyMask     *string `json:"key_mask"`
	ConnectedAt *string `json:"connected_at"`
}

func shownOf(provider websearch.Provider, connected *Connected) shown {
	answer := shown{Provider: string(provider)}
	if connected != nil {
		at := connected.ConnectedAt.UTC().Format(time.RFC3339)
		answer.Connected, answer.KeyMask, answer.ConnectedAt = true, &connected.KeyMask, &at
	}
	return answer
}

// call is one request's common parts.
type call struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	caller    principal.Principal
}

// begin answers a request with no caller and, where person is true, one
// that is not a person in a session. ok is false when it has answered.
func (r Routes) begin(ctx huma.Context, person bool) (call, bool) {
	c := call{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())}
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		r.failed(c, "the route ran without an authenticated caller", nil)
		return c, false
	}
	if person && !caller.IsSession() {
		r.Errors.Write(c.response, http.StatusForbidden, sessionRequired, c.requestID)
		return c, false
	}
	c.caller = caller
	return c, true
}

func (r Routes) failed(c call, reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", c.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(c.ctx.Context(), slog.LevelError, "a web search connection route failed", attributes...)
	r.Errors.Write(c.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, c.requestID)
}

func (r Routes) send(c call, status int, body any) {
	rendered, err := json.Marshal(body)
	if err != nil {
		r.failed(c, "could not render the response", err)
		return
	}
	pythonjson.Send(c.response, status, rendered)
}

func (r Routes) list(ctx huma.Context) {
	c, ok := r.begin(ctx, false)
	if !ok {
		return
	}
	stored, err := r.Rows.Connections(ctx.Context(), c.caller.UserID)
	if err != nil {
		r.failed(c, "could not read the account's web search connections", err)
		return
	}
	answer := struct {
		Providers []shown `json:"providers"`
	}{Providers: make([]shown, 0, len(providers))}
	for _, provider := range providers {
		var held *Connected
		for index := range stored {
			if stored[index].Provider == string(provider) {
				held = &stored[index]
			}
		}
		answer.Providers = append(answer.Providers, shownOf(provider, held))
	}
	r.send(c, http.StatusOK, answer)
}

func (r Routes) connect(ctx huma.Context) {
	c, ok := r.begin(ctx, true)
	if !ok {
		return
	}
	provider, known := providerNamed(ctx.Param("provider"))
	if !known {
		r.Errors.Write(c.response, http.StatusNotFound, unknownProvider, c.requestID)
		return
	}
	body, failure, err := requestbody.Decode(ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if err != nil {
		r.failed(c, "a body reached the route that was never buffered", err)
		return
	}
	if failure != nil {
		r.Errors.WriteValidation(c.response, c.requestID, []errorenvelope.Failure{*failure})
		return
	}
	key := ""
	if body != nil {
		text, isText := body.GetOrNil("api_key").(string)
		if !isText && body.GetOrNil("api_key") != nil {
			r.Errors.WriteValidation(c.response, c.requestID, []errorenvelope.Failure{
				{Location: []string{requestbody.BodyRoot, "api_key"}, Message: requestbody.NotAString}})
			return
		}
		key = strings.TrimSpace(text)
	}
	if refusal := keyRefusal(key); refusal != "" {
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, refusal, c.requestID)
		return
	}

	// Asked before anything is stored: a key the provider refuses is never
	// saved, so the chat is never offered a search that cannot run.
	if err := r.Verifier.Verify(ctx.Context(), provider, key); err != nil {
		status, message := http.StatusBadGateway, provider.Label()+" could not be reached. Try again."
		switch {
		case errors.Is(err, websearch.ErrKeyRejected):
			status, message = http.StatusUnprocessableEntity, provider.Label()+" rejected this key"
		case errors.Is(err, websearch.ErrQuota):
			status, message = http.StatusUnprocessableEntity, "This "+provider.Label()+" account is out of credit or rate limited"
		}
		r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "a web search key was not connected",
			slog.String("request_id", c.requestID), slog.String("provider", string(provider)), slog.String("reason", err.Error()))
		r.Errors.Write(c.response, status, message, c.requestID)
		return
	}
	// Bound to the owner's id in lower case, the one spelling every opener
	// uses, whatever case this request's token carried.
	sealed, err := r.Seal.Seal(strings.ToLower(c.caller.UserID), key)
	if err != nil {
		r.failed(c, "could not seal the key", err)
		return
	}
	stored, err := r.Rows.Connect(ctx.Context(), c.caller.UserID, string(provider), sealed, mcpserver.MaskOf(key))
	if err != nil {
		r.failed(c, "could not store the key", err)
		return
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "web search connected",
		slog.String("user_id", c.caller.UserID), slog.String("provider", string(provider)))
	r.send(c, http.StatusOK, shownOf(provider, &stored))
}

func (r Routes) disconnect(ctx huma.Context) {
	c, ok := r.begin(ctx, true)
	if !ok {
		return
	}
	provider, known := providerNamed(ctx.Param("provider"))
	if !known {
		r.Errors.Write(c.response, http.StatusNotFound, unknownProvider, c.requestID)
		return
	}
	removed, err := r.Rows.Disconnect(ctx.Context(), c.caller.UserID, string(provider))
	if err != nil {
		r.failed(c, "could not remove the key", err)
		return
	}
	if !removed {
		r.Errors.Write(c.response, http.StatusNotFound, notConnected, c.requestID)
		return
	}
	pythonjson.SendNoContent(c.response)
}

// keyRefusal is why a key is refused before the provider is asked, or "".
// A key is one token: a pasted space or line break is a paste gone wrong,
// and would only be refused by the provider in vaguer words.
func keyRefusal(key string) string {
	switch {
	case key == "":
		return keyRequired
	case len(key) > mostKeyBytes:
		return keyMalformed
	}
	for _, character := range key {
		if unicode.IsSpace(character) || unicode.IsControl(character) || character > unicode.MaxASCII {
			return keyMalformed
		}
	}
	return ""
}
