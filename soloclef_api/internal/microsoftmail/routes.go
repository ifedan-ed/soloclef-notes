package microsoftmail

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
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
)

// Routes serves the three routes.
type Routes struct {
	Rows     Rows
	Settings Settings
	Now      func() time.Time
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
	// AppHold keeps a mailbox of a connect the phone app started
	// (internal/apphandoff) until the app confirms it. Nil, such a connect
	// cannot be started (BeginForApp refuses); the website's never needs it.
	AppHold appreturn.Holder

	microsoft *microsoft
}

// checked is what the routes and the sign-in both need of their parts.
func checked(rows Rows, settings Settings, transport http.RoundTripper, now func() time.Time, logger *slog.Logger) error {
	switch {
	case rows == nil || transport == nil || now == nil || logger == nil:
		return errors.New("microsoftmail: the rows, the transport, the clock and the logger are all required")
	case settings.SecretKey == "":
		return errors.New("microsoftmail: the secret key that stored tokens are sealed with is not set")
	}
	if !settings.Configured() {
		return nil
	}
	switch {
	case settings.AuthorizeURL == "" || settings.TokenURL == "":
		return errors.New("microsoftmail: Microsoft's two addresses must be named")
	case settings.FrontendURL == "":
		return errors.New("microsoftmail: Microsoft mail is set up and the website's address (FRONTEND_URL) is not: the callback has nowhere to send the person")
	case cookiePath(settings.RedirectURI) != CallbackPath:
		return errors.New("microsoftmail: MICROSOFT_MAIL_REDIRECT_URI must end in " + CallbackPath + ", the callback this tier serves")
	}
	return nil
}

// NewRoutes checks the parts and builds the routes. transport is the
// process's. A deployment that has not set Microsoft mail up is not an
// error: its routes say so. One that has must have registered this tier's
// callback, because the cookie is scoped to that path.
func NewRoutes(rows Rows, settings Settings, transport http.RoundTripper, now func() time.Time,
	envelope errorenvelope.Writer, logger *slog.Logger) (Routes, error) {
	if err := checked(rows, settings, transport, now, logger); err != nil {
		return Routes{}, err
	}
	return Routes{Rows: rows, Settings: settings, Now: now, Errors: envelope, Logger: logger,
		microsoft: newMicrosoft(settings, transport)}, nil
}

// Register adds the three operations.
func (r Routes) Register(api huma.API) {
	type callback struct {
		Code             string `query:"code"`
		Error            string `query:"error"`
		ErrorDescription string `query:"error_description"`
		State            string `query:"state"`
	}
	stream := func(serve func(huma.Context)) *huma.StreamResponse { return &huma.StreamResponse{Body: serve} }
	add := func(id, method, path, summary, description string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary,
			Description: description, Tags: []string{"connectors"}}
	}
	huma.Register(api, add("microsoft-mail-availability", http.MethodGet, AvailabilityPath, "Whether Microsoft mailboxes can be connected",
		"Whether this deployment has a Microsoft app set up. Only that: no setting is named."),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.availability), nil })
	huma.Register(api, add("microsoft-mail-connect", http.MethodPost, ConnectPath, "Start connecting a Microsoft mailbox",
		"Answers Microsoft's sign-in page. The attempt is bound to this browser by a cookie. A session only."),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.connect), nil })
	huma.Register(api, add("microsoft-mail-callback", http.MethodGet, CallbackPath, "Microsoft's callback",
		"Where Microsoft sends the person back. Saves the mailbox for the browser that started the connect, and redirects to the website."),
		func(context.Context, *callback) (*huma.StreamResponse, error) { return stream(r.callback), nil })
}

// call is one request's common parts.
type call struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	caller    principal.Principal
}

// begin answers a request with no caller (a wiring fault) and, where
// person is true, one that is not a person in a session. ok is false when
// it has answered.
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

// failed is the sanitised 500: the reason goes to the log.
func (r Routes) failed(c call, reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", c.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(c.ctx.Context(), slog.LevelError, "a Microsoft mail route failed", attributes...)
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

// availability is open to the account's API keys too: it says one thing
// about the deployment and nothing about the caller.
func (r Routes) availability(ctx huma.Context) {
	c, ok := r.begin(ctx, false)
	if !ok {
		return
	}
	r.send(c, http.StatusOK, struct {
		Configured bool `json:"configured"`
	}{r.Settings.Configured()})
}

// connect seals a new attempt into this browser's cookie and answers
// Microsoft's sign-in page.
func (r Routes) connect(ctx huma.Context) {
	c, ok := r.begin(ctx, true)
	if !ok {
		return
	}
	if !r.Settings.Configured() {
		r.Errors.WriteCoded(c.response, http.StatusServiceUnavailable, notConfiguredCode, notConfigured, c.requestID)
		return
	}
	// The body is optional: `{"return_path": "/dashboard/…", "account_id":
	// "…"}`, either key, or nothing.
	var body struct {
		ReturnPath *string `json:"return_path"`
		AccountID  *string `json:"account_id"`
	}
	if raw := requestbody.Buffered(ctx.Context()); len(raw) > 0 {
		if json.Unmarshal(raw, &body) != nil {
			r.Errors.Write(c.response, http.StatusUnprocessableEntity, "The body must be a JSON object", c.requestID)
			return
		}
	}
	if (body.ReturnPath != nil && len(*body.ReturnPath) > 512) || (body.AccountID != nil && len(*body.AccountID) > 64) {
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, "return_path is at most 512 characters and account_id at most 64", c.requestID)
		return
	}
	accountID := ""
	if body.AccountID != nil {
		accountID = *body.AccountID
	}
	loginHint, err := r.loginHint(ctx.Context(), c.caller.UserID, accountID)
	if err != nil {
		r.failed(c, "could not read the mailbox being reconnected", err)
		return
	}
	returnPath := ""
	if body.ReturnPath != nil {
		returnPath = *body.ReturnPath
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
	}{r.microsoft.consentURL(begun, loginHint)})
}

// loginHint is the address Microsoft's page preselects when a mailbox is
// reconnected. An id that is not one of the owner's own Microsoft mailboxes
// hints nothing, and the connect goes on as a new one.
func (r Routes) loginHint(ctx context.Context, ownerID, accountID string) (string, error) {
	if accountID == "" {
		return "", nil
	}
	stored, err := r.Rows.Mailbox(ctx, ownerID, accountID)
	if err != nil {
		return "", err
	}
	var held record
	if stored != nil && json.Unmarshal(stored, &held) == nil {
		return held.Text("username"), nil
	}
	return "", nil
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
		// Lax: sent on Microsoft's top-level redirect back to this site.
		SameSite: http.SameSiteLaxMode,
	})
}

// clearAttemptCookie ends the attempt in this browser: every callback
// does, however it ends, so a reload of the callback finds nothing.
func (r Routes) clearAttemptCookie(response http.ResponseWriter) {
	http.SetCookie(response, &http.Cookie{Name: cookieName, Value: "", Path: cookiePath(r.Settings.RedirectURI),
		MaxAge: -1, HttpOnly: true, Secure: strings.HasPrefix(r.Settings.RedirectURI, "https://"), SameSite: http.SameSiteLaxMode})
}

// backToSite ends the callback: the attempt's cookie cleared, and the
// browser sent to the page the connect started on (or the connectors
// page) with `?microsoft_mail=<outcome>`.
func (r Routes) backToSite(c call, returnPath, outcome string) {
	page := returnPath
	if page == "" {
		page = connectorsPage
	}
	separator := "?"
	if strings.Contains(page, "?") {
		separator = "&"
	}
	r.clearAttemptCookie(c.response)
	header := c.response.Header()
	header.Set("Location", strings.TrimRight(r.Settings.FrontendURL, "/")+page+separator+url.Values{outcomeParam: {outcome}}.Encode())
	// The address held Microsoft's code; it is not to be kept or passed on.
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	c.response.WriteHeader(http.StatusSeeOther)
}

// adminApprovalCodes are Microsoft's error numbers for "this organization
// lets only admins consent".
var adminApprovalCodes = []string{"AADSTS65001", "AADSTS90094", "AADSTS90095"}

// refusalFromMicrosoft is the outcome for the error Microsoft's page sent
// the person back with.
func refusalFromMicrosoft(code, description string) string {
	if code == "consent_required" {
		return outcomeAdminApproval
	}
	for _, number := range adminApprovalCodes {
		if strings.Contains(description, number) {
			return outcomeAdminApproval
		}
	}
	if code == "access_denied" {
		return outcomeDenied
	}
	return outcomeFailed
}

// callback is where Microsoft sends the person back. It has no session:
// the attempt's cookie says who is connecting, and only the browser that
// started the connect has it.
func (r Routes) callback(ctx huma.Context) {
	c := call{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())}
	if !r.Settings.Configured() {
		// No website address is promised without the settings, so this one
		// ending is an answer and not a redirect.
		r.Errors.WriteCoded(c.response, http.StatusServiceUnavailable, notConfiguredCode, notConfigured, c.requestID)
		return
	}
	sealed := ""
	if cookie, err := huma.ReadCookie(ctx, cookieName); err == nil {
		sealed = cookie.Value
	}
	now := r.Now()
	begun, err := reopen(sealed, ctx.Query("state"), r.Settings.SecretKey, now)
	if err != nil {
		// Without the attempt nothing says whether the website or the app
		// started it, so it ends where the website's always did.
		r.backToSite(c, "", outcomeExpired)
		return
	}
	// Every ending goes back to whoever started the connect: the website's
	// page, or the phone app's browser sheet (internal/apphandoff), which
	// words the same outcomes.
	end := func(outcome string) {
		if begun.AppChallenge != "" {
			r.backToApp(c, outcome, "")
			return
		}
		r.backToSite(c, begun.ReturnPath, outcome)
	}
	// refuse logs why, in this package's words only: the query values are
	// whatever the address says.
	refuse := func(outcome, reason string) {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "a Microsoft mailbox connect was refused",
			slog.String("request_id", c.requestID), slog.String("outcome", outcome), slog.String("reason", reason))
		end(outcome)
	}
	if said := ctx.Query("error"); said != "" {
		refuse(refusalFromMicrosoft(said, ctx.Query("error_description")), "Microsoft's page returned an error")
		return
	}
	code := ctx.Query("code")
	if code == "" {
		refuse(outcomeFailed, "the callback carries no code")
		return
	}
	// Microsoft's code is spent by the exchange: from here the grant is
	// either saved or lost, so the rest runs to its end even if the
	// browser has gone.
	settle := context.WithoutCancel(ctx.Context())
	granted, err := r.microsoft.redeem(settle, code, begun.Verifier, now)
	if word, dead := revoked(err); dead {
		if word == "consent_required" {
			refuse(outcomeAdminApproval, err.Error())
		} else {
			refuse(outcomeFailed, err.Error())
		}
		return
	}
	if err != nil {
		refuse(outcomeFailed, err.Error())
		return
	}
	if !granted.GrantsIMAP() {
		refuse(outcomeScopeMissing, "IMAP access was not granted")
		return
	}
	if granted.RefreshToken == "" {
		refuse(outcomeNoRefresh, "Microsoft issued no refresh token")
		return
	}
	box, err := mailboxFromIDToken(granted.IDToken, r.Settings.ClientID)
	if err != nil {
		refuse(outcomeNoAddress, err.Error())
		return
	}
	if begun.AppChallenge != "" {
		r.holdForApp(c, settle, begun, box, granted)
		return
	}
	if err := r.saveMailbox(settle, c.requestID, begun.UserID, box, granted); err != nil {
		end(outcomeFailed)
		return
	}
	end(outcomeConnected)
}

// saveMailbox writes a granted mailbox into the owner's mail connection:
// the callback's save for the website and SaveForApp's for the phone app,
// so a mailbox is written one way whoever confirmed it.
func (r Routes) saveMailbox(ctx context.Context, requestID, ownerID string, box mailbox, granted grant) error {
	now := r.Now()
	err := r.Rows.Save(ctx, ownerID, func(accounts []json.RawMessage) ([]json.RawMessage, error) {
		return placed(accounts, box, granted, r.Settings.SecretKey, now)
	})
	if err != nil {
		r.Logger.LogAttrs(ctx, slog.LevelError, "a connected Microsoft mailbox could not be saved",
			slog.String("request_id", requestID), slog.String("error", err.Error()))
		return err
	}
	r.Logger.LogAttrs(ctx, slog.LevelInfo, "Microsoft mailbox connected", slog.String("user_id", ownerID))
	return nil
}
