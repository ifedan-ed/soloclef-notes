package searchconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/mcpserver"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/sealedsecret"
)

// An MCP server's OAuth sign-in, as the MCP authorization specification
// describes it (mcptools/oauth.go speaks the protocol):
//
//	POST /api/v1/connectors/mcp/oauth/discover               does this server offer a sign-in, and must a client id be typed?
//	POST /api/v1/connectors/mcp/{connector_id}/oauth/start   send the person to sign in to a saved server
//	GET  /api/v1/connectors/mcp/oauth/callback               where the authorization server sends them back
//
// A sign-in is always for a saved server: the form saves the server first
// (auth type "oauth", with a client id and secret only when the provider
// does not register clients itself) and then starts the sign-in, so the
// callback has a row to write the tokens to. Every OAuth address is reached
// through the guard a connection to the server goes through.

// The paths.
const (
	OAuthDiscoverPath = "/api/v1/connectors/mcp/oauth/discover"
	OAuthStartPath    = "/api/v1/connectors/mcp/{connector_id}/oauth/start"
	OAuthCallbackPath = "/api/v1/connectors/mcp/oauth/callback"
)

// The outcomes the callback sends the person back with, as
// `?mcp_oauth=<outcome>&mcp_connector=<id>`.
const (
	oauthOutcomeParam     = "mcp_oauth"
	oauthConnectorParam   = "mcp_connector"
	oauthConnected        = "connected"
	oauthDenied           = "denied"
	oauthFailed           = "failed"
	oauthExpired          = "expired"
	oauthConnectorsPage   = "/dashboard/settings/connectors"
	oauthNotConfigured    = "OAuth sign-in to MCP servers is not set up on this server"
	oauthNotConfiguredTag = "mcp_oauth_not_configured"
)

// What the start answers when it cannot send the person anywhere.
const (
	oauthNotOAuth      = "This server is not set to sign in with OAuth"
	oauthNotOffered    = "This server does not offer an OAuth sign-in"
	oauthNeedsClient   = "This server's sign-in needs a client ID: enter the one its provider gave you"
	oauthUnreachable   = "The server's sign-in could not be reached. Try again."
	oauthRefusedPrefix = "This server's sign-in cannot be used: "
)

// OAuthSettings is this deployment's side of a sign-in.
type OAuthSettings struct {
	// RedirectURI is this tier's callback as the world reaches it: the
	// public address of OAuthCallbackPath. Empty, no sign-in is offered.
	RedirectURI string
	// FrontendURL is the website the callback sends the person back to.
	FrontendURL string
}

// Configured reports whether sign-ins can be offered.
func (s OAuthSettings) Configured() bool { return s.RedirectURI != "" }

// SignInWrites stores a finished sign-in.
type SignInWrites interface {
	// StoreSignIn writes the sign-in to the owner's OAuth server, only
	// while it still points at serverURL. stored is false when it does not
	// (removed, moved, or no longer an OAuth server since the sign-in
	// started).
	StoreSignIn(ctx context.Context, ownerID string, connectorID int64, serverURL string, oauth []byte) (stored bool, err error)
}

// OAuthRoutes serves the three routes. All fields are required.
type OAuthRoutes struct {
	Rows     Rows
	Writes   SignInWrites
	Policy   ServerPolicy
	OAuth    mcptools.OAuth
	Attempts sealedsecret.Seal
	Settings OAuthSettings
	Tools    ToolChanges
	Now      func() time.Time
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

// Check reports what is missing or inconsistent. Called once at start-up.
func (r OAuthRoutes) Check() error {
	switch {
	case r.Rows == nil || r.Writes == nil || r.Tools == nil || r.Now == nil || r.Logger == nil:
		return errors.New("searchconnectors: the OAuth routes need their rows, writes, tool changes, clock and logger")
	case !r.Attempts.Ready() || !r.Policy.Seal.Ready():
		return errors.New("searchconnectors: the OAuth routes need their seals")
	case r.OAuth.Check() != nil:
		return r.OAuth.Check()
	}
	if !r.Settings.Configured() {
		return nil
	}
	parsed, err := url.Parse(r.Settings.RedirectURI)
	switch {
	case err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "":
		return errors.New("searchconnectors: MCP_OAUTH_REDIRECT_URI must be an absolute http(s) address")
	case parsed.Path != OAuthCallbackPath:
		return errors.New("searchconnectors: MCP_OAUTH_REDIRECT_URI must end in " + OAuthCallbackPath + ", the callback this tier serves")
	case strings.TrimSpace(r.Settings.FrontendURL) == "":
		return errors.New("searchconnectors: MCP OAuth is set up and the website's address (FRONTEND_URL) is not: the callback has nowhere to send the person")
	}
	return nil
}

// Register adds the three operations.
func (r OAuthRoutes) Register(api huma.API) {
	stream := func(serve func(huma.Context)) *huma.StreamResponse { return &huma.StreamResponse{Body: serve} }
	huma.Register(api, huma.Operation{
		OperationID: "mcp-oauth-discover", Method: http.MethodPost, Path: OAuthDiscoverPath,
		Summary:     "Does an MCP server offer an OAuth sign-in",
		Description: "Discovers the server's authorization server, and says whether it registers clients itself. A session only.",
		Tags:        []string{"connectors"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.discover), nil })
	huma.Register(api, huma.Operation{
		OperationID: "mcp-oauth-start", Method: http.MethodPost, Path: OAuthStartPath,
		Summary:     "Start signing in to a saved MCP server",
		Description: "Answers the authorization server's sign-in page. The attempt is bound to this browser by a cookie. A session only.",
		Tags:        []string{"connectors"},
	}, func(context.Context, *struct {
		ConnectorID string `path:"connector_id" doc:"The MCP server's id"`
	}) (*huma.StreamResponse, error) {
		return stream(r.start), nil
	})
	huma.Register(api, huma.Operation{
		OperationID: "mcp-oauth-callback", Method: http.MethodGet, Path: OAuthCallbackPath,
		Summary:     "An MCP server's OAuth callback",
		Description: "Where the authorization server sends the person back. Stores the sign-in for the browser that started it, and redirects to the website.",
		Tags:        []string{"connectors"},
	}, func(context.Context, *struct {
		Code  string `query:"code"`
		State string `query:"state"`
		Error string `query:"error"`
	}) (*huma.StreamResponse, error) {
		return stream(r.callback), nil
	})
}

// call is one request's common parts.
type oauthCall struct {
	failed    failures
	response  http.ResponseWriter
	requestID string
	caller    principal.Principal
}

// begin answers a request with no caller and one that is not a person in a
// session. ok is false when it has answered.
func (r OAuthRoutes) begin(ctx huma.Context, message string) (oauthCall, bool) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	c := oauthCall{response: response, requestID: requestID,
		failed: failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response, requestID: requestID, message: message}}
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		c.failed.caught(noCaller, nil)
		return c, false
	}
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, sessionRequired, requestID)
		return c, false
	}
	if !r.Settings.Configured() {
		r.Errors.WriteCoded(response, http.StatusServiceUnavailable, oauthNotConfiguredTag, oauthNotConfigured, requestID)
		return c, false
	}
	c.caller = caller
	return c, true
}

// discover serves POST …/oauth/discover: {"url": …, "connector_id"?: …}.
// Like a test, every failure but "no sign-in offered" is answered in the
// same words, and why is in the log.
func (r OAuthRoutes) discover(ctx huma.Context) {
	c, ok := r.begin(ctx, "the MCP server's sign-in could not be discovered")
	if !ok {
		return
	}
	body, problems, err := readBody(ctx)
	if err != nil {
		c.failed.caught("a body reached the route that was never buffered", err)
		return
	}
	address, saved := "", (*number)(nil)
	if body != nil {
		if text, isText := body.GetOrNil("url").(string); isText {
			address = strings.TrimSpace(text)
		}
		if raw, present := body.Get("connector_id"); present && raw != nil {
			digits, isNumber := raw.(pythonjson.RawInt)
			if !isNumber {
				problems = append(problems, bodyFailure("connector_id must be a number", "connector_id"))
			} else {
				id, parseErr := strconv.ParseInt(string(digits), 10, 64)
				saved = &number{value: id, tooWide: parseErr != nil}
			}
		}
	}
	if address == "" {
		problems = append(problems, bodyFailure(addressRequired, "url"))
	}
	if len(problems) > 0 {
		r.Errors.WriteValidation(c.response, c.requestID, problems)
		return
	}

	origin := ""
	if saved != nil {
		var server *Connection
		if !saved.tooWide {
			if server, err = r.Rows.Server(ctx.Context(), c.caller.UserID, saved.value); err != nil {
				c.failed.caught(rowUnread, err)
				return
			}
		}
		if server == nil {
			r.Errors.Write(c.response, http.StatusNotFound, serverAbsent, c.requestID)
			return
		}
		origin = originOf(*server)
		// The form shows a saved address masked; sent back unchanged, it
		// stands for the stored one.
		if stored, found := storedServerOf(*server); found && address == stored.URL {
			if address, err = mcpserver.OpenAddress(r.Policy.Seal, c.caller.UserID, stored); err != nil {
				c.failed.caught("could not open the server's address", err)
				return
			}
		}
	} else if origin, err = r.Policy.originForNew(ctx.Context(), c.caller.UserID); err != nil {
		c.failed.caught("could not read the caller's standing", err)
		return
	}
	answer := struct {
		Status              string `json:"status"`
		Message             string `json:"message,omitempty"`
		AuthorizationServer string `json:"authorization_server,omitempty"`
		DynamicRegistration bool   `json:"dynamic_registration"`
	}{Status: "error", Message: oauthUnreachable}
	hosts, err := r.Policy.oauthHosts(ctx.Context(), origin)
	if err != nil {
		c.failed.caught("could not read the site's allowed hosts", err)
		return
	}
	found, err := r.OAuth.Discover(ctx.Context(), address, hosts)
	switch {
	case errors.Is(err, mcptools.ErrNoOAuth):
		answer.Message = oauthNotOffered
	case err != nil:
		r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "an MCP server's sign-in was not discovered",
			slog.String("request_id", c.requestID), slog.String("user_id", c.caller.UserID), slog.String("reason", err.Error()))
	default:
		answer.Status, answer.Message = "success", ""
		answer.AuthorizationServer, answer.DynamicRegistration = found.Issuer, found.RegistrationEndpoint != ""
	}
	r.send(c, http.StatusOK, answer)
}

// start serves POST …/{connector_id}/oauth/start: {"return_path"?: …}.
func (r OAuthRoutes) start(ctx huma.Context) {
	c, ok := r.begin(ctx, "the MCP server's sign-in could not be started")
	if !ok {
		return
	}
	problems := make([]errorenvelope.Failure, 0, 1)
	connector := readNumber(ctx.Param("connector_id"), &problems, "path", "connector_id")
	body, bodyProblems, err := readBody(ctx)
	if err != nil {
		c.failed.caught("a body reached the route that was never buffered", err)
		return
	}
	problems = append(problems, bodyProblems...)
	returnPath := ""
	if body != nil {
		if text, isText := body.GetOrNil("return_path").(string); isText {
			returnPath = safeReturnPath(text)
		}
	}
	if len(problems) > 0 {
		r.Errors.WriteValidation(c.response, c.requestID, problems)
		return
	}
	var server *Connection
	if !connector.tooWide {
		if server, err = r.Rows.Server(ctx.Context(), c.caller.UserID, connector.value); err != nil {
			c.failed.caught(rowUnread, err)
			return
		}
	}
	if server == nil {
		r.Errors.Write(c.response, http.StatusNotFound, serverAbsent, c.requestID)
		return
	}
	stored, found := storedServerOf(*server)
	if !found || stored.Auth.Type != mcpserver.AuthOAuth {
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, oauthNotOAuth, c.requestID)
		return
	}
	origin := originOf(*server)
	hosts, err := r.Policy.oauthHosts(ctx.Context(), origin)
	if err != nil {
		c.failed.caught("could not read the site's allowed hosts", err)
		return
	}
	address, err := mcpserver.OpenAddress(r.Policy.Seal, c.caller.UserID, stored)
	if err != nil {
		c.failed.caught("could not open the server's address", err)
		return
	}
	discovered, err := r.OAuth.Discover(ctx.Context(), address, hosts)
	if err != nil {
		r.refuseStart(c, ctx.Context(), err)
		return
	}
	client, dynamic, err := r.client(ctx.Context(), c.caller.UserID, stored.Auth.OAuth, discovered, hosts)
	if err != nil {
		r.refuseStart(c, ctx.Context(), err)
		return
	}
	pkce, err := mcptools.NewPKCE()
	if err != nil {
		c.failed.caught("no randomness for the proof key", err)
		return
	}
	state, err := newState()
	if err != nil {
		c.failed.caught("no randomness for the state", err)
		return
	}
	page, err := mcptools.AuthorizationURL(discovered.AuthorizationEndpoint, client.ID, r.Settings.RedirectURI,
		state, pkce.Challenge, discovered.Resource, discovered.Scope)
	if err != nil {
		r.refuseStart(c, ctx.Context(), err)
		return
	}
	attempt := oauthAttempt{
		Verifier: pkce.Verifier, UserID: c.caller.UserID, ConnectorID: server.ID, ServerURL: stored.URL,
		Issuer: discovered.Issuer, TokenEndpoint: discovered.TokenEndpoint, RevocationEndpoint: discovered.RevocationEndpoint,
		Resource: discovered.Resource, Scope: discovered.Scope,
		ClientID: client.ID, ClientAuth: client.AuthMethod, Dynamic: dynamic, ReturnPath: returnPath,
		Expires: r.Now().Add(oauthAttemptLifetime).Unix(),
	}
	if dynamic {
		attempt.ClientSecret = client.Secret
	}
	sealed, err := sealAttempt(r.Attempts, state, attempt)
	if err != nil {
		c.failed.caught("could not seal the sign-in attempt", err)
		return
	}
	http.SetCookie(c.response, &http.Cookie{
		Name: oauthCookieName, Value: sealed, Path: cookiePathOf(r.Settings.RedirectURI),
		MaxAge: int(oauthAttemptLifetime / time.Second), HttpOnly: true,
		Secure: strings.HasPrefix(r.Settings.RedirectURI, "https://"),
		// Lax: the cookie rides the top-level redirect back from the
		// provider's page, and no request another site makes.
		SameSite: http.SameSiteLaxMode,
	})
	r.send(c, http.StatusOK, struct {
		AuthorizationURL string `json:"authorization_url"`
	}{page})
}

// client is who this deployment signs in as: the client the person typed,
// the one registered before with this authorization server, or one
// registered now when the server offers registration.
func (r OAuthRoutes) client(ctx context.Context, ownerID string, stored *mcpserver.StoredOAuth, discovered mcptools.Discovered, hosts mcptools.HostsFor) (mcptools.Client, bool, error) {
	if stored.ClientID != "" && (!stored.DynamicRegistration || stored.Issuer == discovered.Issuer) {
		secret, err := mcpserver.OpenClientSecret(r.Policy.Seal, ownerID, stored)
		if err != nil {
			return mcptools.Client{}, false, err
		}
		method := stored.ClientAuth
		if !stored.DynamicRegistration || method == "" {
			if method, err = mcptools.ClientAuthFor(secret, discovered.AuthMethods); err != nil {
				return mcptools.Client{}, false, err
			}
		}
		return mcptools.Client{ID: stored.ClientID, Secret: secret, AuthMethod: method}, stored.DynamicRegistration, nil
	}
	if discovered.RegistrationEndpoint == "" {
		return mcptools.Client{}, false, errNeedsClient
	}
	client, err := r.OAuth.Register(ctx, discovered, r.Settings.RedirectURI, hosts)
	return client, true, err
}

var errNeedsClient = errors.New("searchconnectors: the authorization server does not register clients and none was typed")

// refuseStart answers a start that cannot send the person anywhere, in a
// sentence for the form, and logs why.
func (r OAuthRoutes) refuseStart(c oauthCall, ctx context.Context, err error) {
	var refused *mcptools.Refusal
	status, message := http.StatusBadGateway, oauthUnreachable
	switch {
	case errors.Is(err, mcptools.ErrNoOAuth):
		status, message = http.StatusUnprocessableEntity, oauthNotOffered
	case errors.Is(err, errNeedsClient):
		status, message = http.StatusUnprocessableEntity, oauthNeedsClient
	case errors.As(err, &refused):
		status, message = http.StatusUnprocessableEntity, oauthRefusedPrefix+refused.Reason
	}
	r.Logger.LogAttrs(ctx, slog.LevelInfo, "an MCP sign-in was not started",
		slog.String("request_id", c.requestID), slog.String("user_id", c.caller.UserID), slog.String("reason", err.Error()))
	r.Errors.Write(c.response, status, message, c.requestID)
}

// callback serves GET …/oauth/callback. It has no session: the attempt's
// cookie says who is signing in, and only the browser that started the
// sign-in has it.
func (r OAuthRoutes) callback(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	if !r.Settings.Configured() {
		r.Errors.WriteCoded(response, http.StatusServiceUnavailable, oauthNotConfiguredTag, oauthNotConfigured, requestID)
		return
	}
	sealed := ""
	if cookie, err := huma.ReadCookie(ctx, oauthCookieName); err == nil {
		sealed = cookie.Value
	}
	attempt, err := openAttempt(r.Attempts, sealed, ctx.Query("state"), r.Now())
	if err != nil {
		r.backToSite(response, "", 0, oauthExpired)
		return
	}
	end := func(outcome, reason string) {
		if reason != "" {
			r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "an MCP sign-in did not finish",
				slog.String("request_id", requestID), slog.Int64("connector_id", attempt.ConnectorID),
				slog.String("outcome", outcome), slog.String("reason", reason))
		}
		r.backToSite(response, attempt.ReturnPath, attempt.ConnectorID, outcome)
	}
	if said := ctx.Query("error"); said != "" {
		if said == "access_denied" {
			end(oauthDenied, "the person declined")
			return
		}
		end(oauthFailed, "the authorization server returned an error")
		return
	}
	code := ctx.Query("code")
	if code == "" {
		end(oauthFailed, "the callback carries no code")
		return
	}
	// The code is spent by the exchange: from here the sign-in is either
	// stored or lost, so the rest runs to its end even if the browser goes.
	settle := context.WithoutCancel(ctx.Context())
	server, err := r.Rows.Server(settle, attempt.UserID, attempt.ConnectorID)
	if err != nil || server == nil {
		end(oauthFailed, "the server was removed while the person signed in")
		return
	}
	stored, found := storedServerOf(*server)
	if !found || stored.Auth.Type != mcpserver.AuthOAuth || stored.URL != attempt.ServerURL {
		end(oauthFailed, "the server was changed while the person signed in")
		return
	}
	hosts, err := r.Policy.oauthHosts(settle, originOf(*server))
	if err != nil {
		end(oauthFailed, err.Error())
		return
	}
	client := mcptools.Client{ID: attempt.ClientID, Secret: attempt.ClientSecret, AuthMethod: attempt.ClientAuth}
	if !attempt.Dynamic {
		// A typed client's secret stays in the row, not the cookie.
		if client.Secret, err = mcpserver.OpenClientSecret(r.Policy.Seal, attempt.UserID, stored.Auth.OAuth); err != nil {
			end(oauthFailed, err.Error())
			return
		}
	}
	granted, err := r.OAuth.Exchange(settle, attempt.TokenEndpoint, client, code, attempt.Verifier,
		r.Settings.RedirectURI, attempt.Resource, hosts)
	if err != nil {
		end(oauthFailed, err.Error())
		return
	}
	signIn, err := r.signedIn(attempt, stored.Auth.OAuth, granted)
	if err != nil {
		end(oauthFailed, err.Error())
		return
	}
	written, err := r.Writes.StoreSignIn(settle, attempt.UserID, attempt.ConnectorID, attempt.ServerURL, signIn)
	if err != nil || !written {
		reason := "the server was changed while the person signed in"
		if err != nil {
			reason = err.Error()
		}
		end(oauthFailed, reason)
		return
	}
	// The server's tools are listed again with the sign-in, at the
	// account's next turn.
	toolsChanged(settle, r.Tools, r.Logger, requestID, attempt.UserID)
	r.Logger.LogAttrs(settle, slog.LevelInfo, "an MCP server was signed in to",
		slog.String("user_id", attempt.UserID), slog.Int64("connector_id", attempt.ConnectorID))
	end(oauthConnected, "")
}

// signedIn is the stored sign-in: the client, the endpoints and the
// tokens, sealed.
func (r OAuthRoutes) signedIn(attempt oauthAttempt, previous *mcpserver.StoredOAuth, granted mcptools.Granted) ([]byte, error) {
	tokens, err := mcpserver.SealTokens(r.Policy.Seal, attempt.UserID, mcpserver.Tokens{
		AccessToken: granted.AccessToken, RefreshToken: granted.RefreshToken,
	})
	if err != nil {
		return nil, err
	}
	signIn := mcpserver.StoredOAuth{
		ClientID: attempt.ClientID, ClientSecret: previous.ClientSecret, DynamicRegistration: attempt.Dynamic,
		ClientAuth: attempt.ClientAuth, Issuer: attempt.Issuer, TokenEndpoint: attempt.TokenEndpoint,
		RevocationEndpoint: attempt.RevocationEndpoint, Resource: attempt.Resource, Scope: attempt.Scope, Tokens: tokens,
	}
	if attempt.Dynamic {
		signIn.ClientSecret = nil
		if attempt.ClientSecret != "" {
			secret, err := mcpserver.SealSecret(r.Policy.Seal, attempt.UserID, attempt.ClientSecret)
			if err != nil {
				return nil, err
			}
			signIn.ClientSecret = &secret
		}
	}
	if granted.ExpiresIn > 0 {
		signIn.ExpiresAt = r.Now().Unix() + granted.ExpiresIn
	}
	return json.Marshal(signIn)
}

// backToSite ends the callback: the attempt's cookie cleared and the
// browser sent to the page the sign-in started on, or the connectors page.
func (r OAuthRoutes) backToSite(response http.ResponseWriter, returnPath string, connectorID int64, outcome string) {
	page := returnPath
	if page == "" {
		page = oauthConnectorsPage
	}
	separator := "?"
	if strings.Contains(page, "?") {
		separator = "&"
	}
	query := url.Values{oauthOutcomeParam: {outcome}}
	if connectorID != 0 {
		query.Set(oauthConnectorParam, strconv.FormatInt(connectorID, 10))
	}
	http.SetCookie(response, &http.Cookie{Name: oauthCookieName, Value: "", Path: cookiePathOf(r.Settings.RedirectURI),
		MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(r.Settings.RedirectURI, "https://"), SameSite: http.SameSiteLaxMode})
	header := response.Header()
	header.Set("Location", strings.TrimRight(r.Settings.FrontendURL, "/")+page+separator+query.Encode())
	// The address held the provider's code; it is not to be kept or passed on.
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	response.WriteHeader(http.StatusSeeOther)
}

func (r OAuthRoutes) send(c oauthCall, status int, body any) {
	rendered, err := json.Marshal(body)
	if err != nil {
		c.failed.uncaught(bodyUnwritten, err)
		return
	}
	pythonjson.Send(c.response, status, rendered)
}
