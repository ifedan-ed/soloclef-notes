package driveconnect

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/appreturn"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivegrant"
)

// Routes serves the seven routes.
type Routes struct {
	Rows     Rows
	Settings Settings
	Now      func() time.Time
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
	// AppHold keeps a grant of a connect the phone app started
	// (internal/apphandoff) until the app confirms it. Nil, such a connect
	// cannot be started (BeginForApp refuses); the website's never needs it.
	AppHold appreturn.Holder

	google *google
}

// NewRoutes checks the parts and builds the routes. transport is the process's.
// A deployment that has not set Drive up is not an error: its routes say
// which settings are missing. One that has must have registered this
// tier's callback, because the cookie is scoped to that path.
func NewRoutes(rows Rows, settings Settings, transport http.RoundTripper, now func() time.Time,
	envelope errorenvelope.Writer, logger *slog.Logger) (Routes, error) {
	switch {
	case rows == nil || transport == nil || now == nil || logger == nil:
		return Routes{}, errors.New("driveconnect: the rows, the transport, the clock and the logger are all required")
	case settings.SecretKey == "":
		return Routes{}, errors.New("driveconnect: the secret key that stored tokens are sealed with is not set")
	case settings.AuthURL == "" || settings.TokenURL == "" || settings.RevokeURL == "" || settings.AboutURL == "":
		return Routes{}, errors.New("driveconnect: Google's four addresses must be named")
	}
	if len(settings.Missing()) == 0 {
		if settings.FrontendURL == "" {
			return Routes{}, errors.New("driveconnect: Google Drive is set up and the website's address (FRONTEND_URL) is not: the callback has nowhere to send the person")
		}
		if cookiePath(settings.RedirectURI) != CallbackPath {
			return Routes{}, errors.New("driveconnect: GOOGLE_DRIVE_REDIRECT_URI must end in " + CallbackPath + ", the callback this tier serves")
		}
	}
	return Routes{Rows: rows, Settings: settings, Now: now, Errors: envelope, Logger: logger,
		google: newGoogle(settings, transport)}, nil
}

// Register adds the seven operations.
func (r Routes) Register(api huma.API) {
	type connector struct {
		ConnectorID string `path:"connector_id" doc:"The connection's id"`
	}
	type reauth struct {
		ConnectorID string `query:"connector_id" doc:"The connection to reconnect"`
		ReturnURL   string `query:"return_url" doc:"A path of the website to land on afterwards"`
	}
	type callback struct {
		Code  string `query:"code"`
		Error string `query:"error"`
		State string `query:"state"`
	}
	stream := func(serve func(huma.Context)) *huma.StreamResponse { return &huma.StreamResponse{Body: serve} }
	add := func(id, method, path, summary, description string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary,
			Description: description, Tags: []string{"connectors"}}
	}
	huma.Register(api, add("google-drive-connection", http.MethodGet, ConnectionPath, "The caller's Google Drive connection",
		"Whether this deployment can connect Drive (and which settings are missing), and the caller's connection if any."),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.status), nil })
	huma.Register(api, add("google-drive-connect", http.MethodPost, ConnectPath, "Start connecting Google Drive",
		"Answers Google's consent page. The attempt is bound to this browser by a cookie."),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.connect), nil })
	huma.Register(api, add("google-drive-connect-from-list", http.MethodGet, AddPath, "Start connecting Google Drive, from the connectors list",
		"The same start as the POST, landing on the connectors page."),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.connectFromList), nil })
	huma.Register(api, add("google-drive-reconnect", http.MethodGet, ReauthPath, "Reconnect Google Drive",
		"Starts a new consent for a connection Google stopped accepting. The grant replaces the one in the same row."),
		func(context.Context, *reauth) (*huma.StreamResponse, error) { return stream(r.reconnect), nil })
	huma.Register(api, add("google-drive-callback", http.MethodGet, CallbackPath, "Google's callback",
		"Where Google sends the person back. Saves the grant for the browser that started the connect, and redirects to the website."),
		func(context.Context, *callback) (*huma.StreamResponse, error) { return stream(r.callback), nil })
	huma.Register(api, add("google-drive-picker-session", http.MethodGet, PickerPath, "A Google Picker session",
		"A fresh, short-lived access token for the Google Picker, for the connection's owner."),
		func(context.Context, *connector) (*huma.StreamResponse, error) { return stream(r.picker), nil })
	huma.Register(api, add("google-drive-disconnect", http.MethodDelete, DisconnectPath, "Disconnect Google Drive",
		"Removes the caller's connection and ends its grant at Google. Imported files stay."),
		func(context.Context, *connector) (*huma.StreamResponse, error) { return stream(r.disconnect), nil })
}

// call is one request's common parts.
type call struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	caller    principal.Principal
}

// begin answers a request with no caller (a wiring fault) or one that is
// not a person in a session. ok is false when it has answered.
func (r Routes) begin(ctx huma.Context) (call, bool) {
	c := call{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())}
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		r.failed(c, "the route ran without an authenticated caller", nil)
		return c, false
	}
	if !caller.IsSession() {
		r.Errors.Write(c.response, http.StatusForbidden, sessionRequired, c.requestID)
		return c, false
	}
	c.caller = caller
	return c, true
}

// failed is the sanitised 500: the reason goes to the log.
func (r Routes) failed(c call, reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", c.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(c.ctx.Context(), slog.LevelError, "a Google Drive connection route failed", attributes...)
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

// unconfigured answers the 503 of a deployment that cannot connect Drive
// and reports whether it did.
func (r Routes) unconfigured(c call) bool {
	if len(r.Settings.Missing()) == 0 {
		return false
	}
	r.Errors.WriteCoded(c.response, http.StatusServiceUnavailable, notConfiguredCode, notConfigured, c.requestID)
	return true
}

// connectorID reads the path's id; ok is false for anything that is not
// an id a connection could have.
func connectorID(text string) (int64, bool) {
	id, err := strconv.ParseInt(text, 10, 64)
	return id, err == nil && id > 0
}

func (r Routes) status(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok {
		return
	}
	held, err := r.Rows.Connection(ctx.Context(), c.caller.UserID)
	if err != nil {
		r.failed(c, "could not read the connection", err)
		return
	}
	missing := r.Settings.Missing()
	view := struct {
		Configured     bool     `json:"configured"`
		Missing        []string `json:"missing"`
		ConnectorID    *int64   `json:"connector_id"`
		AccountEmail   *string  `json:"account_email"`
		NeedsReconnect bool     `json:"needs_reconnect"`
	}{Configured: len(missing) == 0, Missing: missing}
	if held != nil {
		stored := drivegrant.Read(held.Config)
		view.ConnectorID = &held.ID
		if stored.AccountEmail != "" {
			view.AccountEmail = &stored.AccountEmail
		}
		view.NeedsReconnect = stored.AuthExpired || stored.RefreshToken == ""
	}
	r.send(c, http.StatusOK, view)
}

// start seals a new attempt into this browser's cookie and answers
// Google's consent page.
func (r Routes) start(c call, returnPath string) {
	if r.unconfigured(c) {
		return
	}
	now := r.Now()
	begun, err := newAttempt(c.caller.UserID, returnPath, now)
	var sealed string
	if err == nil {
		sealed, err = begun.seal(r.Settings.SecretKey, now)
	}
	if err != nil {
		r.failed(c, "could not start the connect attempt", err)
		return
	}
	r.setAttemptCookie(c.response, sealed)
	r.send(c, http.StatusOK, struct {
		AuthURL string `json:"auth_url"`
	}{r.google.consentURL(begun)})
}

// setAttemptCookie binds an attempt to the browser of this response: the
// website's Connect, and the phone app's hand-off page in its browser sheet.
func (r Routes) setAttemptCookie(response http.ResponseWriter, sealed string) {
	http.SetCookie(response, &http.Cookie{
		Name: cookieName, Value: sealed,
		// The callback is the only request the cookie goes with.
		Path:     cookiePath(r.Settings.RedirectURI),
		MaxAge:   int(attemptLifetime / time.Second),
		HttpOnly: true,
		Secure:   strings.HasPrefix(r.Settings.RedirectURI, "https://"),
		// Lax: sent on Google's top-level redirect back to this site.
		SameSite: http.SameSiteLaxMode,
	})
}

// clearAttemptCookie ends the attempt in this browser: every callback
// does, however it ends, so a reload of the callback finds nothing.
func (r Routes) clearAttemptCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{Name: cookieName, Value: "", Path: cookiePath(r.Settings.RedirectURI),
		MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(r.Settings.RedirectURI, "https://"), SameSite: http.SameSiteLaxMode})
}

func (r Routes) connect(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok {
		return
	}
	// The body is optional: `{"return_path": "/dashboard/…"}` or nothing.
	returnPath := ""
	if raw := requestbody.Buffered(ctx.Context()); len(raw) > 0 {
		var body struct {
			ReturnPath *string `json:"return_path"`
		}
		if json.Unmarshal(raw, &body) != nil {
			r.Errors.Write(c.response, http.StatusUnprocessableEntity, "The body must be a JSON object", c.requestID)
			return
		}
		if body.ReturnPath != nil {
			returnPath = *body.ReturnPath
		}
	}
	r.start(c, returnPath)
}

func (r Routes) connectFromList(ctx huma.Context) {
	if c, ok := r.begin(ctx); ok {
		r.start(c, "")
	}
}

func (r Routes) reconnect(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok {
		return
	}
	var held *connection
	if id, valid := connectorID(ctx.Query("connector_id")); valid {
		var err error
		if held, err = r.Rows.ConnectionByID(ctx.Context(), c.caller.UserID, id); err != nil {
			r.failed(c, "could not read the connection", err)
			return
		}
	}
	if held == nil {
		r.Errors.Write(c.response, http.StatusNotFound, connectionAbsent, c.requestID)
		return
	}
	r.start(c, ctx.Query("return_url"))
}

// backToSite ends the callback: the attempt's cookie cleared, and the
// browser sent to the website with the outcome. A connect started from a
// page lands back on it with `?google_drive=<outcome>`; one started from
// the connectors list lands on the list's own callback page.
func (r Routes) backToSite(c call, returnPath, outcome string, connectorID int64) {
	target := "/dashboard/connectors/callback?"
	query := url.Values{"connector": {"google-drive-connector"}}
	if returnPath != "" {
		target = returnPath + "?"
		if strings.Contains(returnPath, "?") {
			target = returnPath + "&"
		}
		query = url.Values{"google_drive": {outcome}}
	} else if outcome == outcomeConnected {
		query.Set("success", "true")
		query.Set("connectorId", strconv.FormatInt(connectorID, 10))
	} else {
		query.Set("error", outcome)
	}
	r.clearAttemptCookie(c.response)
	header := c.response.Header()
	header.Set("Location", strings.TrimRight(r.Settings.FrontendURL, "/")+target+query.Encode())
	// The address held Google's code; it is not to be kept or passed on.
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	c.response.WriteHeader(http.StatusSeeOther)
}

// callback is where Google sends the person back. It has no session: the
// attempt's cookie says who is connecting, and only the browser that
// started the connect has it.
func (r Routes) callback(ctx huma.Context) {
	c := call{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())}
	if len(r.Settings.Missing()) > 0 {
		r.Errors.WriteCoded(c.response, http.StatusServiceUnavailable, notConfiguredCode, notConfigured, c.requestID)
		return
	}
	sealed := ""
	if cookie, err := huma.ReadCookie(ctx, cookieName); err == nil {
		sealed = cookie.Value
	}
	begun, err := reopen(sealed, ctx.Query("state"), r.Settings.SecretKey, r.Now())
	if err != nil {
		// Without the attempt nothing says whether the website or the app
		// started it, so it ends where the website's always did.
		r.backToSite(c, "", outcomeExpired, 0)
		return
	}
	// Every ending goes back to whoever started the connect: the website's
	// page, or the phone app's browser sheet (internal/apphandoff), which
	// words the same outcomes.
	end := func(outcome string, connectorID int64) {
		if begun.AppChallenge != "" {
			r.backToApp(c, outcome, "")
			return
		}
		r.backToSite(c, begun.ReturnPath, outcome, connectorID)
	}
	code := ctx.Query("code")
	if ctx.Query("error") != "" || code == "" {
		end(outcomeDenied, 0)
		return
	}
	// Google's code is spent by the exchange: from here the grant is
	// either saved or lost, so the rest runs to its end even if the
	// browser has gone.
	settle := context.WithoutCancel(ctx.Context())
	granted, err := r.google.exchange(settle, code, begun.Verifier)
	if err != nil {
		r.Logger.LogAttrs(settle, slog.LevelWarn, "a Google Drive code exchange failed",
			slog.String("request_id", c.requestID), slog.String("error", err.Error()))
		end(outcomeFailed, 0)
		return
	}
	if !slices.Contains(granted.Scopes, driveFileScope) {
		r.google.revoke(settle, granted.AccessToken)
		end(outcomeNoScope, 0)
		return
	}
	if granted.RefreshToken == "" {
		r.google.revoke(settle, granted.AccessToken)
		end(outcomeNoRefresh, 0)
		return
	}
	email := r.google.accountEmail(settle, granted.AccessToken)
	if begun.AppChallenge != "" {
		r.holdForApp(c, settle, begun, granted, email)
		return
	}
	id, err := r.saveGrant(settle, c.requestID, begun.UserID, granted, email)
	if err != nil {
		end(outcomeFailed, 0)
		return
	}
	end(outcomeConnected, id)
}

// saveGrant writes a consent's grant into the account's one Drive
// connection and retires the grant it replaced. It is the callback's save
// for the website and SaveForApp's for the phone app, so a connection is
// written one way whoever confirmed it. A grant that cannot be saved is
// revoked at once: nothing would ever use or end it.
func (r Routes) saveGrant(ctx context.Context, requestID, userID string, granted grant, email string) (int64, error) {
	name := "Google Drive"
	if email != "" {
		name += " (" + email + ")"
	}
	// The name column's width.
	if len(name) > 100 {
		name = "Google Drive"
	}
	now := r.Now()
	id, previous, err := r.Rows.Save(ctx, userID, name, now.Add(resyncMinutes*time.Minute),
		func(previous []byte) ([]byte, error) { return storedGrant(previous, granted, email, r.Settings, now) })
	if err != nil {
		r.google.revoke(ctx, granted.RefreshToken)
		r.Logger.LogAttrs(ctx, slog.LevelError, "a Google Drive grant could not be saved",
			slog.String("request_id", requestID), slog.String("error", err.Error()))
		return 0, err
	}
	// The grant this one replaced is of no further use; retire it.
	if old, readable := drivegrant.Read(previous).OpenRefreshToken(r.Settings.SecretKey); readable && old != granted.RefreshToken {
		r.google.revoke(ctx, old)
	}
	r.Logger.LogAttrs(ctx, slog.LevelInfo, "Google Drive connected",
		slog.String("user_id", userID), slog.Int64("connector_id", id))
	return id, nil
}

func (r Routes) picker(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok {
		return
	}
	var held *connection
	if id, valid := connectorID(ctx.Param("connector_id")); valid {
		var err error
		if held, err = r.Rows.ConnectionByID(ctx.Context(), c.caller.UserID, id); err != nil {
			r.failed(c, "could not read the connection", err)
			return
		}
	}
	if held == nil {
		r.Errors.Write(c.response, http.StatusNotFound, connectionAbsent, c.requestID)
		return
	}
	if r.unconfigured(c) {
		return
	}
	fresh, err := r.pickerToken(ctx.Context(), c.requestID, c.caller.UserID, held)
	if errors.Is(err, errInvalidGrant) {
		r.Errors.WriteCoded(c.response, http.StatusBadRequest, reconnectCode, reconnect, c.requestID)
		return
	}
	if err != nil {
		r.Errors.Write(c.response, http.StatusBadGateway, googleSilent, c.requestID)
		return
	}
	// The one credential this package hands out, and to its owner only:
	// short lived, and under `drive.file` good only for the files they
	// pick. The API key is a browser key, public by design.
	c.response.Header().Set("Cache-Control", "no-store")
	r.send(c, http.StatusOK, struct {
		AccessToken string `json:"access_token"`
		APIKey      string `json:"api_key"`
		AppID       string `json:"app_id"`
	}{fresh.AccessToken, r.Settings.PickerAPIKey, r.Settings.pickerAppID()})
}

// pickerToken is a fresh access token for the owner's connection: the
// website's Picker session and the phone app's Picker page (PickerFor).
// errInvalidGrant is Google refusing the stored grant, which is then
// remembered, so the settings page offers to connect again without anyone
// having to try the Picker first. Any other error is Google unreachable,
// already logged.
func (r Routes) pickerToken(ctx context.Context, requestID, ownerID string, held *connection) (grant, error) {
	refreshToken, readable := drivegrant.Read(held.Config).OpenRefreshToken(r.Settings.SecretKey)
	var fresh grant
	err := errInvalidGrant
	if readable {
		fresh, err = r.google.refresh(ctx, refreshToken)
	}
	if errors.Is(err, errInvalidGrant) {
		if err := r.Rows.MarkExpired(context.WithoutCancel(ctx), ownerID, held.ID); err != nil {
			r.Logger.LogAttrs(ctx, slog.LevelWarn, "a refused Drive grant could not be flagged",
				slog.String("request_id", requestID), slog.String("error", err.Error()))
		}
		return grant{}, errInvalidGrant
	}
	if err != nil {
		r.Logger.LogAttrs(ctx, slog.LevelWarn, "refreshing a Drive token failed",
			slog.String("request_id", requestID), slog.String("error", err.Error()))
		return grant{}, err
	}
	return fresh, nil
}

func (r Routes) disconnect(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok {
		return
	}
	var (
		config []byte
		found  bool
	)
	if id, valid := connectorID(ctx.Param("connector_id")); valid {
		var err error
		if config, found, err = r.Rows.Delete(ctx.Context(), c.caller.UserID, id); err != nil {
			r.failed(c, "could not remove the connection", err)
			return
		}
	}
	if !found {
		r.Errors.Write(c.response, http.StatusNotFound, connectionAbsent, c.requestID)
		return
	}
	// After the row is gone, and whether or not the browser still waits:
	// a removed connection should be dead at Google too.
	revoked := false
	if token, readable := drivegrant.Read(config).OpenRefreshToken(r.Settings.SecretKey); readable {
		revoked = r.google.revoke(context.WithoutCancel(ctx.Context()), token)
	}
	r.send(c, http.StatusOK, struct {
		Revoked bool `json:"revoked"`
	}{revoked})
}
