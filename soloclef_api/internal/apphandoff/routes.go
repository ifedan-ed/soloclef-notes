package apphandoff

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/appreturn"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/driveconnect"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/microsoftmail"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Drive is what the hand-off needs of internal/driveconnect.
type Drive interface {
	Configured() bool
	UsableConnection(ctx context.Context, ownerID string) (int64, error)
	PickerFor(ctx context.Context, requestID, ownerID string) (driveconnect.PickerSession, error)
	BeginForApp(response http.ResponseWriter, ownerID, challenge string) (string, error)
	SaveForApp(ctx context.Context, requestID, ownerID, sealed string) (int64, error)
}

// Microsoft is what the hand-off needs of internal/microsoftmail.
type Microsoft interface {
	Configured() bool
	BeginForApp(ctx context.Context, response http.ResponseWriter, ownerID, accountID, challenge string) (string, error)
	SaveForApp(ctx context.Context, requestID, ownerID, sealed string) error
}

// Importer is internal/drivesync's import, made for an account named by a
// ticket rather than by a session.
type Importer interface {
	ImportFor(ctx huma.Context, userID string, connectorID int64, body io.Reader)
}

// Notebooks says whether a notebook is the account's own (drivesync.Store).
type Notebooks interface {
	OwnsNotebook(ctx context.Context, userID string, notebookID int64) (bool, error)
}

// Accounts says whether an account may still act (principal's directory):
// the page and the import have no session, so nothing else would notice an
// account deactivated in the minutes since the code was asked for.
type Accounts interface {
	UserIsActive(ctx context.Context, userID string) (bool, error)
}

// Routes serves the four routes.
type Routes struct {
	Secrets   Secrets
	Drive     Drive
	Microsoft Microsoft
	Imports   Importer
	Notebooks Notebooks
	Accounts  Accounts
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
}

// Check reports a part that is missing. Called once at start-up.
func (r Routes) Check() error {
	if r.Secrets.Store == nil || r.Drive == nil || r.Microsoft == nil || r.Imports == nil ||
		r.Notebooks == nil || r.Accounts == nil || r.Logger == nil {
		return errors.New("apphandoff: the secrets, Drive, Microsoft, the import, the notebooks, the accounts and the logger are all required")
	}
	return nil
}

// Register adds the four operations.
func (r Routes) Register(api huma.API) {
	type page struct {
		Code string `path:"code" doc:"The hand-off's one-time code"`
		For  string `query:"for" doc:"drive_picker for the Picker's page, so a spent code still gets the Picker's own answer"`
	}
	stream := func(serve func(huma.Context)) *huma.StreamResponse { return &huma.StreamResponse{Body: serve} }
	add := func(id, method, path, summary, description string) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary,
			Description: description, Tags: []string{"app"}}
	}
	huma.Register(api, add("app-handoff-start", http.MethodPost, StartPath, "A one-time code for the app's browser sheet",
		"60 seconds, single use, one purpose: drive_picker, drive_connect or microsoft_mail. A session only."),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.start), nil })
	huma.Register(api, add("app-handoff-page", http.MethodGet, PagePath, "The hand-off's page",
		"Spends the code and starts its flow in this browser: the Picker's page, or the provider's sign-in."),
		func(context.Context, *page) (*huma.StreamResponse, error) { return stream(r.page), nil })
	huma.Register(api, add("app-handoff-complete", http.MethodPost, CompletePath, "Save what an app's connect held",
		"The completion the sheet came back with and the verifier behind the code's challenge, from the account that asked."),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.complete), nil })
	huma.Register(api, add("app-handoff-drive-picker-import", http.MethodPost, ImportPath, "The Picker page's import",
		"The page's ticket stands for the account, its Drive connection and the notebook; it imports once."),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.importPicked), nil })
}

// call is one request's common parts.
type call struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
}

func (r Routes) open(ctx huma.Context) call {
	return call{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())}
}

// person is the caller of a gated route, who must be a person in a
// session: an API key never starts or confirms a browser flow, as on the
// website's own connect routes. ok is false when it has answered.
func (r Routes) person(c call) (principal.Principal, bool) {
	caller, authenticated := principal.FromContext(c.ctx.Context())
	if !authenticated {
		r.failed(c, "the route ran without an authenticated caller", nil)
		return caller, false
	}
	if !caller.IsSession() {
		r.Errors.Write(c.response, http.StatusForbidden, "This action requires an interactive session", c.requestID)
		return caller, false
	}
	return caller, true
}

// failed is the sanitised 500: the reason goes to the log.
func (r Routes) failed(c call, reason string, cause error) {
	r.log(c, slog.LevelError, reason, cause)
	r.Errors.Write(c.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, c.requestID)
}

func (r Routes) log(c call, level slog.Level, reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", c.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(c.ctx.Context(), level, "an app hand-off route", attributes...)
}

// unavailable is Valkey not answering: nothing can be issued or spent.
func (r Routes) unavailable(c call, cause error) {
	r.log(c, slog.LevelError, "the hand-off's secrets could not be reached", cause)
	r.Errors.Write(c.response, http.StatusServiceUnavailable, "The browser hand-off is unavailable right now. Try again in a moment.", c.requestID)
}

func (r Routes) send(c call, body any) {
	rendered, err := json.Marshal(body)
	if err != nil {
		r.failed(c, "could not render the response", err)
		return
	}
	// A code or a completion is a credential: never kept by a cache.
	c.response.Header().Set("Cache-Control", "no-store")
	pythonjson.Send(c.response, http.StatusOK, rendered)
}

// smallBody bounds the two JSON bodies a session sends here; each is a few
// short fields.
const smallBody = 4 << 10

// readJSON reads a small body into into, refusing unknown keys: a client
// that sends one believes it is asking for something. ok is false when it
// has answered.
func (r Routes) readJSON(c call, limit int64, into any, wrong string) bool {
	raw, err := io.ReadAll(io.LimitReader(c.ctx.BodyReader(), limit+1))
	if err != nil {
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, "The request body could not be read", c.requestID)
		return false
	}
	if int64(len(raw)) > limit {
		r.Errors.Write(c.response, http.StatusRequestEntityTooLarge, "The request body is too large", c.requestID)
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(into) != nil || decoder.More() {
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, wrong, c.requestID)
		return false
	}
	return true
}

// s256Challenge is RFC 7636's: 43 characters of base64url, the encoding of
// a SHA-256.
var s256Challenge = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

// verifierText is RFC 7636's code verifier: 43 to 128 unreserved characters.
var verifierText = regexp.MustCompile(`^[A-Za-z0-9._~-]{43,128}$`)

// verifierMatches reports whether verifier is the one behind challenge, in
// constant time.
func verifierMatches(verifier, challenge string) bool {
	if !verifierText.MatchString(verifier) {
		return false
	}
	digest := sha256.Sum256([]byte(verifier))
	return subtle.ConstantTimeCompare([]byte(base64.RawURLEncoding.EncodeToString(digest[:])), []byte(challenge)) == 1
}

// start issues a code, after the website's own checks for the flow, so a
// refusal the website would give (Drive not set up, not connected) is told
// to the app before the sheet opens.
func (r Routes) start(ctx huma.Context) {
	c := r.open(ctx)
	caller, ok := r.person(c)
	if !ok {
		return
	}
	var body struct {
		Purpose       *string `json:"purpose"`
		NotebookID    *int64  `json:"notebook_id"`
		AccountID     *string `json:"account_id"`
		CodeChallenge *string `json:"code_challenge"`
	}
	if !r.readJSON(c, smallBody, &body, "The request body must be a JSON object with a purpose") {
		return
	}
	switch {
	case body.Purpose == nil || *body.Purpose == "":
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, "purpose is required", c.requestID)
		return
	case !supported[*body.Purpose]:
		r.Errors.WriteCoded(c.response, http.StatusBadRequest, codeUnsupported, "The app can't hand this off to the browser yet", c.requestID)
		return
	case body.NotebookID != nil && *body.NotebookID < 1:
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, "notebook_id must be a notebook's number", c.requestID)
		return
	case body.AccountID != nil && len(*body.AccountID) > 64:
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, "account_id is at most 64 characters", c.requestID)
		return
	case body.CodeChallenge != nil && *body.CodeChallenge != "" && !s256Challenge.MatchString(*body.CodeChallenge):
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, "code_challenge must be an S256 challenge", c.requestID)
		return
	}
	purpose := *body.Purpose
	issued := grant{Purpose: purpose, UserID: caller.UserID}
	switch purpose {
	case appreturn.DrivePicker:
		if !r.pickerAllowed(c, caller.UserID, body.NotebookID) {
			return
		}
		issued.NotebookID = body.NotebookID
	case appreturn.DriveConnect:
		if !r.Drive.Configured() {
			r.Errors.WriteCoded(c.response, http.StatusServiceUnavailable, codeDriveNotConfigured, "Google Drive is not set up on this server", c.requestID)
			return
		}
	case appreturn.MicrosoftMail:
		if !r.Microsoft.Configured() {
			r.Errors.WriteCoded(c.response, http.StatusServiceUnavailable, codeMicrosoftNotConfigured, "Microsoft accounts aren't set up on this server", c.requestID)
			return
		}
		if body.AccountID != nil {
			issued.AccountID = *body.AccountID
		}
	}
	if confirmed[purpose] {
		// After the provider's own checks, so a server without the
		// provider set up says so even to an app that sends no challenge.
		if body.CodeChallenge == nil || *body.CodeChallenge == "" {
			what := "Google Drive"
			if purpose == appreturn.MicrosoftMail {
				what = "a Microsoft account"
			}
			r.Errors.WriteCoded(c.response, http.StatusBadRequest, codeChallengeRequired,
				"Update the app to connect "+what+" from it, or connect it on the website", c.requestID)
			return
		}
		issued.Challenge = *body.CodeChallenge
	}
	code, err := r.Secrets.issue(ctx.Context(), codes, issued)
	if err != nil {
		r.unavailable(c, err)
		return
	}
	path := "/api/v1/app/handoff/" + code
	// The Picker's page names its purpose, so a spent code still gets the
	// Picker's own answer (a page) instead of the connectors' (a return).
	if purpose == appreturn.DrivePicker {
		path += "?for=" + appreturn.DrivePicker
	}
	r.send(c, struct {
		Code      string `json:"code"`
		ExpiresIn int    `json:"expires_in"`
		Path      string `json:"path"`
	}{code, int(codeLifetime.Seconds()), path})
}

// pickerAllowed is the Picker's checks: Drive set up, the notebook the
// account's own, and a connection Google still accepts. ok is false when
// it has answered.
func (r Routes) pickerAllowed(c call, userID string, notebookID *int64) bool {
	if !r.Drive.Configured() {
		r.Errors.WriteCoded(c.response, http.StatusServiceUnavailable, codeDriveNotConfigured, "Google Drive is not set up on this server", c.requestID)
		return false
	}
	if notebookID != nil {
		owned, err := r.Notebooks.OwnsNotebook(c.ctx.Context(), userID, *notebookID)
		if err != nil {
			r.failed(c, "could not read the notebook", err)
			return false
		}
		if !owned {
			// Another account's notebook and no notebook: one answer.
			r.Errors.Write(c.response, http.StatusNotFound, "Notebook not found", c.requestID)
			return false
		}
	}
	_, err := r.Drive.UsableConnection(c.ctx.Context(), userID)
	switch {
	case errors.Is(err, driveconnect.ErrNotConnected):
		r.Errors.WriteCoded(c.response, http.StatusNotFound, codeDriveNotConnected, "Connect Google Drive first", c.requestID)
		return false
	case errors.Is(err, driveconnect.ErrReconnect):
		r.Errors.WriteCoded(c.response, http.StatusBadRequest, codeDriveReconnect, "Google no longer accepts this connection; connect again", c.requestID)
		return false
	case err != nil:
		r.failed(c, "could not read the Drive connection", err)
		return false
	}
	return true
}

// redirect sends the browser on, telling it to keep nothing and pass no
// Referer: the address it came from named the code, and where it goes may
// hold a completion.
func redirect(c call, status int, location string) {
	header := c.response.Header()
	header.Set("Location", location)
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	c.response.WriteHeader(status)
}

// page is the browser sheet's: it spends the code and starts the flow the
// code is for, in this browser.
func (r Routes) page(ctx huma.Context) {
	c := r.open(ctx)
	picker := ctx.Query("for") == appreturn.DrivePicker
	issued, err := r.Secrets.redeem(ctx.Context(), codes, ctx.Param("code"))
	if err != nil {
		r.log(c, slog.LevelError, "the hand-off's secrets could not be reached", err)
		if picker {
			r.messagePage(c, http.StatusServiceUnavailable, "Soloclef can't open this right now",
				"Close this page and try again in the app in a moment.")
			return
		}
		redirect(c, http.StatusSeeOther, appreturn.ConnectorReturn("", appreturn.StatusFailed, ""))
		return
	}
	if issued == nil {
		if picker {
			r.messagePage(c, http.StatusGone, "This link has run out",
				"A link from the app opens once, within a minute. Close this page and try again in the app.")
			return
		}
		redirect(c, http.StatusSeeOther, appreturn.ConnectorReturn("", appreturn.StatusExpired, ""))
		return
	}
	switch issued.Purpose {
	case appreturn.DrivePicker:
		r.openPicker(c, *issued)
	case appreturn.DriveConnect:
		if !r.Drive.Configured() {
			redirect(c, http.StatusSeeOther, appreturn.ConnectorReturn(issued.Purpose, appreturn.StatusNotConfigured, ""))
			return
		}
		if issued.Challenge == "" {
			// Never issued without one (start); refused all the same.
			redirect(c, http.StatusSeeOther, appreturn.ConnectorReturn(issued.Purpose, appreturn.StatusExpired, ""))
			return
		}
		consent, err := r.Drive.BeginForApp(c.response, issued.UserID, issued.Challenge)
		r.toProvider(c, issued.Purpose, consent, err)
	case appreturn.MicrosoftMail:
		if !r.Microsoft.Configured() {
			redirect(c, http.StatusSeeOther, appreturn.ConnectorReturn(issued.Purpose, appreturn.StatusNotConfigured, ""))
			return
		}
		if issued.Challenge == "" {
			redirect(c, http.StatusSeeOther, appreturn.ConnectorReturn(issued.Purpose, appreturn.StatusExpired, ""))
			return
		}
		signIn, err := r.Microsoft.BeginForApp(ctx.Context(), c.response, issued.UserID, issued.AccountID, issued.Challenge)
		r.toProvider(c, issued.Purpose, signIn, err)
	default:
		redirect(c, http.StatusSeeOther, appreturn.ConnectorReturn("", appreturn.StatusExpired, ""))
	}
}

// toProvider sends the browser to the provider's page, the attempt's
// cookie already set on this response; or back to the app when the
// attempt could not be made.
func (r Routes) toProvider(c call, purpose, location string, err error) {
	if err != nil {
		r.log(c, slog.LevelError, "an app's connect could not start", err)
		redirect(c, http.StatusSeeOther, appreturn.ConnectorReturn(purpose, appreturn.StatusFailed, ""))
		return
	}
	redirect(c, http.StatusSeeOther, location)
}

// backToDrive ends the Picker's hand-off before its page: back to the app
// saying why.
func backToDrive(c call, why string) {
	redirect(c, http.StatusFound, appreturn.DriveReturn(url.Values{"error": {why}}))
}

// openPicker is the Picker's page with a ticket for its one import, after
// the start's checks are made again for the account the code stands for:
// a minute may have changed the connection, the notebook or the account.
func (r Routes) openPicker(c call, issued grant) {
	ctx := c.ctx.Context()
	active, err := r.Accounts.UserIsActive(ctx, issued.UserID)
	if err != nil {
		r.log(c, slog.LevelError, "could not read the account", err)
		backToDrive(c, "picker_failed")
		return
	}
	if !active {
		backToDrive(c, "signed_out")
		return
	}
	if issued.NotebookID != nil {
		owned, err := r.Notebooks.OwnsNotebook(ctx, issued.UserID, *issued.NotebookID)
		if err != nil || !owned {
			r.log(c, slog.LevelInfo, "the Picker's notebook is gone or could not be read", err)
			backToDrive(c, "picker_failed")
			return
		}
	}
	session, err := r.Drive.PickerFor(ctx, c.requestID, issued.UserID)
	switch {
	case errors.Is(err, driveconnect.ErrReconnect):
		backToDrive(c, "google_drive_reconnect")
		return
	case err != nil:
		r.log(c, slog.LevelInfo, "the Picker's session could not be made", err)
		backToDrive(c, "picker_failed")
		return
	}
	ticket, err := r.Secrets.issue(ctx, tickets, grant{Purpose: appreturn.DrivePicker, UserID: issued.UserID,
		NotebookID: issued.NotebookID, ConnectorID: session.ConnectorID})
	if err != nil {
		r.log(c, slog.LevelError, "the Picker's ticket could not be issued", err)
		backToDrive(c, "picker_failed")
		return
	}
	r.pickerPage(c, session, ticket)
}

// importBody bounds the Picker page's body: the import's own limit
// (fifty picks with long names) and the ticket.
const importBody = 256<<10 + 1<<10

// importPicked is the Picker page's one import. No cookie and no bearer:
// the ticket is the credential, and it is in the page's body only, so no
// other site can make this request for the person.
func (r Routes) importPicked(ctx huma.Context) {
	c := r.open(ctx)
	var body struct {
		Ticket *string          `json:"ticket"`
		Files  *json.RawMessage `json:"files"`
	}
	if !r.readJSON(c, importBody, &body, "The request body must be a JSON object with the page's ticket and the picked files") {
		return
	}
	if body.Ticket == nil || body.Files == nil {
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, "The page's ticket and the picked files are both required", c.requestID)
		return
	}
	held, err := r.Secrets.redeem(ctx.Context(), tickets, *body.Ticket)
	if err != nil {
		r.unavailable(c, err)
		return
	}
	expired := func() {
		r.Errors.WriteCoded(c.response, http.StatusGone, codeExpired, "This page has run out; pick again in the app", c.requestID)
	}
	if held == nil || held.Purpose != appreturn.DrivePicker || held.ConnectorID < 1 {
		expired()
		return
	}
	if active, err := r.Accounts.UserIsActive(ctx.Context(), held.UserID); err != nil || !active {
		expired()
		return
	}
	// The website's own import body: the ticket says the notebook, so a
	// page cannot name another.
	forwarded, err := json.Marshal(struct {
		Files      json.RawMessage `json:"files"`
		NotebookID *int64          `json:"notebook_id,omitempty"`
	}{*body.Files, held.NotebookID})
	if err != nil {
		r.failed(c, "could not prepare the import", err)
		return
	}
	ctx.SetHeader("Cache-Control", "no-store")
	r.Imports.ImportFor(ctx, held.UserID, held.ConnectorID, bytes.NewReader(forwarded))
}

// complete saves what an app's connect held, for the app that asked. The
// completion is spent by its first try, right or wrong; it saves only with
// the verifier behind the code's challenge, from the account that asked.
func (r Routes) complete(ctx huma.Context) {
	c := r.open(ctx)
	caller, ok := r.person(c)
	if !ok {
		return
	}
	var body struct {
		Completion   *string `json:"completion"`
		CodeVerifier *string `json:"code_verifier"`
	}
	if !r.readJSON(c, smallBody, &body, "The request body must be a JSON object with the completion and the code verifier") {
		return
	}
	if body.Completion == nil || len(*body.Completion) < 20 || len(*body.Completion) > 200 ||
		body.CodeVerifier == nil || !verifierText.MatchString(*body.CodeVerifier) {
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, "completion and a code_verifier of 43 to 128 characters are required", c.requestID)
		return
	}
	held, err := r.Secrets.redeem(ctx.Context(), completions, *body.Completion)
	if err != nil {
		r.unavailable(c, err)
		return
	}
	if held == nil || !confirmed[held.Purpose] || held.Challenge == "" || held.Sealed == "" {
		r.Errors.WriteCoded(c.response, http.StatusGone, codeExpired, "This took too long; connect again", c.requestID)
		return
	}
	if !verifierMatches(*body.CodeVerifier, held.Challenge) {
		r.log(c, slog.LevelWarn, "a completion with a verifier that is not its own", nil)
		r.Errors.WriteCoded(c.response, http.StatusForbidden, codeWrongVerifier, "This connection wasn't started here; connect again", c.requestID)
		return
	}
	if held.UserID != caller.UserID {
		r.log(c, slog.LevelWarn, "a completion spent by another account than the one that asked", nil)
		r.Errors.WriteCoded(c.response, http.StatusForbidden, codeWrongAccount,
			"This was connected through another account's link; nothing was saved", c.requestID)
		return
	}
	// From here the grant is saved or lost, whether or not the app still
	// waits for the answer.
	settle := context.WithoutCancel(ctx.Context())
	what := "Google Drive"
	if held.Purpose == appreturn.DriveConnect {
		_, err = r.Drive.SaveForApp(settle, c.requestID, caller.UserID, held.Sealed)
	} else {
		what = "the mailbox"
		err = r.Microsoft.SaveForApp(settle, c.requestID, caller.UserID, held.Sealed)
	}
	switch {
	case errors.Is(err, driveconnect.ErrNotConfigured):
		r.Errors.WriteCoded(c.response, http.StatusServiceUnavailable, codeDriveNotConfigured, "Google Drive is not set up on this server", c.requestID)
		return
	case errors.Is(err, microsoftmail.ErrNotConfigured):
		r.Errors.WriteCoded(c.response, http.StatusServiceUnavailable, codeMicrosoftNotConfigured, "Microsoft accounts aren't set up on this server", c.requestID)
		return
	case errors.Is(err, driveconnect.ErrHeldUnreadable), errors.Is(err, microsoftmail.ErrHeldUnreadable):
		r.log(c, slog.LevelWarn, "a held grant does not open", err)
		r.Errors.WriteCoded(c.response, http.StatusGone, codeExpired, "This took too long; connect again", c.requestID)
		return
	case err != nil:
		r.log(c, slog.LevelError, "saving what an app's connect held failed", err)
		r.Errors.WriteCoded(c.response, http.StatusInternalServerError, codeSaveFailed, "Couldn't save "+what+"; connect again", c.requestID)
		return
	}
	r.Logger.LogAttrs(settle, slog.LevelInfo, "connected from the app",
		slog.String("request_id", c.requestID), slog.String("purpose", held.Purpose), slog.String("user_id", caller.UserID))
	r.send(c, struct {
		Purpose string `json:"purpose"`
		Status  string `json:"status"`
	}{held.Purpose, "connected"})
}
