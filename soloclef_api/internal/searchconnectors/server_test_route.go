package searchconnectors

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
)

// ServerProbe connects to a server once and lists its tools.
// mcptools.Probe in a deployment.
type ServerProbe interface {
	Tools(ctx context.Context, address, transport string, headers map[string]string, hosts []string) ([]mcptools.ProbedTool, error)
}

// testFailed is the one sentence every failed test is answered with.
const testFailed = "Could not connect to the MCP server. Check the URL and key."

// testNeedsSignIn is a test of an OAuth server before anyone has signed in:
// there is no token to test with. It says nothing about the server.
const testNeedsSignIn = "Sign in to this server first: the test connects with your sign-in."

// ServerTestRoute serves POST /api/v1/connectors/mcp/test: connect to a
// server the form describes, before it is saved, and list its tools.
//
// The body is the server's settings, with `connector_id` beside them when
// the form was opened on a saved server: its masked keys then stand for
// the stored ones, under the same condition as an edit.
//
// What it must not become is a way to ask this process what it can reach.
// So it is for a session only; the connection goes through the same guard
// as every call (the address's own public host for a person's server, the
// site's list for an administrator's); and every failure, whether the
// address was refused, the name did not resolve, the server did not answer
// or the key was wrong, is the same 200 with the same sentence. Why it
// failed is in the log.
//
// Changed from the Python: a `connector_id` that is not the caller's
// server is 404 instead of being ignored, and the tool list is followed
// past its first page.
type ServerTestRoute struct {
	Rows   Rows
	Policy ServerPolicy
	Probe  ServerProbe
	// OAuth and Tokens renew a saved OAuth server's access token when it
	// has run out, as the chat would (mcptools.FreshTokens).
	OAuth  mcptools.OAuth
	Tokens mcptools.TokenRows
	Now    func() time.Time
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r ServerTestRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "test-mcp-connector",
		Method:      http.MethodPost,
		Path:        ServerTestPath,
		Summary:     "Test an MCP server",
		Description: "Connects to the server the body describes and lists its tools. " +
			"A session only. Every failure is answered in the same words.",
		Tags: []string{"connectors"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

// testedTool is one tool in the answer, in the shape the form reads.
type testedTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type testAnswer struct {
	Status  string       `json:"status"`
	Message string       `json:"message"`
	Tools   []testedTool `json:"tools"`
}

func (r ServerTestRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the MCP server could not be tested"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.caught(noCaller, nil)
		return
	}
	body, problems, err := readBody(ctx)
	if err != nil {
		failed.caught("a body reached the route that was never buffered", err)
		return
	}
	// `connector_id` is taken out before the settings are read strictly.
	var saved *number
	settingsBody := pythonjson.NewObject()
	if body != nil {
		for _, key := range body.Keys() {
			value, _ := body.Get(key)
			if key != "connector_id" {
				settingsBody.Set(key, value)
				continue
			}
			if value == nil {
				continue
			}
			digits, isNumber := value.(pythonjson.RawInt)
			if !isNumber {
				problems = append(problems, bodyFailure("connector_id must be a number", "connector_id"))
				continue
			}
			id, parseErr := strconv.ParseInt(string(digits), 10, 64)
			saved = &number{value: id, tooWide: parseErr != nil}
		}
	}
	if len(problems) > 0 {
		r.Errors.WriteValidation(response, requestID, problems)
		return
	}
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, sessionRequired, requestID)
		return
	}
	settings, refusal := readServerSettings(settingsBody)
	if refusal != "" {
		r.Errors.Write(response, http.StatusUnprocessableEntity, refusal, requestID)
		return
	}

	var (
		previous *mcpserver.Stored
		origin   string
		savedID  int64
	)
	if saved != nil {
		var server *Connection
		if !saved.tooWide {
			if server, err = r.Rows.Server(ctx.Context(), caller.UserID, saved.value); err != nil {
				failed.caught(rowUnread, err)
				return
			}
		}
		if server == nil {
			r.Errors.Write(response, http.StatusNotFound, serverAbsent, requestID)
			return
		}
		if held, found := storedServerOf(*server); found {
			previous = &held
		}
		origin, savedID = originOf(*server), server.ID
	} else if origin, err = r.Policy.originForNew(ctx.Context(), caller.UserID); err != nil {
		failed.caught("could not read the caller's standing", err)
		return
	}

	answer := testAnswer{Status: "error", Message: testFailed, Tools: []testedTool{}}
	why := ""
	// The form's settings in their stored form, masks standing for what is
	// stored, and then opened for this one connection: the very path a
	// saved server takes to the chat, so a test that passes is a server
	// that will work.
	stored, refusal, err := sealSettings(r.Policy.Seal, caller.UserID, settings, previous)
	if err != nil {
		failed.caught("could not seal the server's settings", err)
		return
	}
	var hosts []string
	if refusal == "" {
		if hosts, refusal, err = r.Policy.hostsFor(ctx.Context(), origin, settings.Address); err != nil {
			failed.caught("could not read the site's allowed hosts", err)
			return
		}
	}
	var tokens *mcpserver.Tokens
	if refusal == "" && stored.Auth.Type == mcpserver.AuthOAuth {
		if saved == nil || !stored.Auth.OAuth.Authorized() {
			answer.Message, refusal = testNeedsSignIn, "the OAuth server has no sign-in yet"
		} else if tokens, refusal, err = r.tokens(ctx.Context(), caller.UserID, savedID, origin, stored); err != nil {
			failed.caught("could not renew the server's sign-in", err)
			return
		}
	}
	if refusal != "" {
		why = refusal
	} else if target, err := mcpserver.Open(r.Policy.Seal, caller.UserID, stored, tokens); err != nil {
		why = "the stored settings could not be opened: " + err.Error()
	} else if tools, err := r.Probe.Tools(ctx.Context(), target.Address, stored.Transport, target.Headers, hosts); err != nil {
		// The error is the library's or the guard's and neither quotes a
		// header. The address is not logged: it can hold the key.
		why = err.Error()
	} else {
		answer.Status = "success"
		answer.Message = "Connected successfully. Found " + strconv.Itoa(len(tools)) + " tools."
		for _, tool := range tools {
			answer.Tools = append(answer.Tools, testedTool{Name: tool.Name, Description: tool.Description, InputSchema: tool.InputSchema})
		}
	}
	if why != "" {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "an MCP server test failed",
			slog.String("request_id", requestID), slog.String("user_id", caller.UserID), slog.String("reason", why))
	}
	rendered, err := json.Marshal(answer)
	if err != nil {
		failed.uncaught(bodyUnwritten, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, rendered)
}

// tokens is a saved OAuth server's current tokens for a test, renewed
// first when they have run out. A sign-in that has ended is a refusal, in
// the test's own words.
func (r ServerTestRoute) tokens(ctx context.Context, ownerID string, connectorID int64, origin string, stored mcpserver.Stored) (*mcpserver.Tokens, string, error) {
	hosts, err := r.Policy.oauthHosts(ctx, origin)
	if err != nil {
		return nil, "", err
	}
	tokens, err := mcptools.FreshTokens(ctx, mcptools.TokenRenewal{
		OAuth: r.OAuth, Seal: r.Policy.Seal, Rows: r.Tokens, Now: r.Now().Unix(), Hosts: hosts,
	}, ownerID, connectorID, stored.Auth.OAuth)
	if mcptools.IsSignInNeeded(err) {
		return nil, "the OAuth sign-in has ended", nil
	}
	if err != nil {
		// Unreachable or refusing: a failed test like any other.
		return nil, "the OAuth sign-in could not be renewed: " + err.Error(), nil
	}
	return tokens, "", nil
}

// PolicyRoute serves GET /api/v1/connectors/mcp/policy: what the "add an
// MCP server" form needs to know before it is filled in. `can_add` is
// whether this caller may add one at all (a person in a session), and
// `allow_list_applies` whether the server would be a site server, held to
// the site's allowed hosts (an administrator's), or the caller's own, and
// `oauth_available` whether an OAuth sign-in can be offered at all.
type PolicyRoute struct {
	Policy ServerPolicy
	// OAuthAvailable is whether this deployment can sign in to an OAuth
	// server: it names the callback the authorization server sends the
	// person back to (MCP_OAUTH_REDIRECT_URI).
	OAuthAvailable bool
	Errors         errorenvelope.Writer
	Logger         *slog.Logger
}

func (r PolicyRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "mcp-connector-policy",
		Method:      http.MethodGet,
		Path:        ServerPolicyPath,
		Summary:     "Who may add an MCP server",
		Description: "Whether the caller may add an MCP server, and whether the site's " +
			"list of allowed hosts applies to one they add.",
		Tags: []string{"connectors"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r PolicyRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the MCP policy could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.caught(noCaller, nil)
		return
	}
	answer := struct {
		CanAdd           bool `json:"can_add"`
		AllowListApplies bool `json:"allow_list_applies"`
		OAuthAvailable   bool `json:"oauth_available"`
	}{CanAdd: caller.IsSession(), OAuthAvailable: r.OAuthAvailable}
	if answer.CanAdd {
		administers, err := r.Policy.administers(ctx.Context(), caller.UserID)
		if err != nil {
			failed.caught("could not read the caller's standing", err)
			return
		}
		answer.AllowListApplies = administers
	}
	rendered, err := json.Marshal(answer)
	if err != nil {
		failed.uncaught(bodyUnwritten, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, rendered)
}
