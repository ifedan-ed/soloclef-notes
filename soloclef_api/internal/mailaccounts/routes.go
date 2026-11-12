package mailaccounts

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// Routes serves the five routes. Every field is required.
type Routes struct {
	Rows Rows
	Mail Prober
	// Microsoft gives a stored Microsoft mailbox an access token that
	// works (internal/microsoftmail's SignIn).
	Microsoft MicrosoftSignIn
	// SecretKey is the deployment's secret key, which every stored
	// credential is sealed with.
	SecretKey string
	Now       func() time.Time
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (r Routes) Check() error {
	if r.Rows == nil || r.Mail == nil || r.Microsoft == nil || r.SecretKey == "" || r.Now == nil || r.Logger == nil {
		return errors.New("mailaccounts: Rows, Mail, Microsoft, SecretKey, Now and Logger are all required")
	}
	return nil
}

// Register adds the five operations.
func (r Routes) Register(api huma.API) {
	type account struct {
		AccountID string `path:"account_id" doc:"The mailbox's id"`
	}
	stream := func(serve func(huma.Context)) *huma.StreamResponse { return &huma.StreamResponse{Body: serve} }
	huma.Register(api, huma.Operation{
		OperationID: "list-email-accounts", Method: http.MethodGet, Path: AccountsPath,
		Summary:     "The caller's mailboxes",
		Description: "The mailboxes the account has connected, oldest first. Never a password.",
		Tags:        []string{"connectors"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.list), nil })
	huma.Register(api, huma.Operation{
		OperationID: "add-email-account", Method: http.MethodPost, Path: AccountsPath,
		DefaultStatus: http.StatusCreated,
		Summary:       "Add a mailbox",
		Description:   "Adds an IMAP mailbox with an app password, which is stored sealed. A session only.",
		Tags:          []string{"connectors"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.add), nil })
	huma.Register(api, huma.Operation{
		OperationID: "delete-email-account", Method: http.MethodDelete, Path: AccountPath,
		DefaultStatus: http.StatusNoContent,
		Summary:       "Remove a mailbox",
		Description:   "Removes one of the caller's mailboxes and its stored password.",
		Tags:          []string{"connectors"},
	}, func(context.Context, *account) (*huma.StreamResponse, error) { return stream(r.remove), nil })
	huma.Register(api, huma.Operation{
		OperationID: "test-email-account", Method: http.MethodPost, Path: AccountTestPath,
		Summary:     "Test a saved mailbox",
		Description: "Signs in to one of the caller's mailboxes and counts its folders. A session only.",
		Tags:        []string{"connectors"},
	}, func(context.Context, *account) (*huma.StreamResponse, error) { return stream(r.testSaved), nil })
	huma.Register(api, huma.Operation{
		OperationID: "test-unsaved-email-account", Method: http.MethodPost, Path: TestPath,
		Summary:     "Test mailbox settings",
		Description: "Signs in with settings from the form, before they are saved. Nothing is stored. A session only.",
		Tags:        []string{"connectors"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.testUnsaved), nil })
}

// MicrosoftSignIn is how a mailbox connected through Microsoft is signed
// in to: it has no password, and its access token lasts an hour.
type MicrosoftSignIn interface {
	// Mailbox is the settings to sign in to one of ownerID's stored
	// Microsoft mailboxes with, its token refreshed if it was about to
	// lapse. A *ConnectionError is a mailbox that cannot be signed in to,
	// worded for its owner.
	Mailbox(ctx context.Context, ownerID string, record []byte) (Settings, error)
}

// stored is a mailbox record as the row holds it.
type stored struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	UseTLS   *bool  `json:"use_tls,omitempty"`
	Username string `json:"username"`
	// AppPassword is the sealed password. It is in no answer.
	AppPassword string `json:"app_password,omitempty"`
	Encrypted   bool   `json:"_encrypted,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	// Auth is "microsoft" for a mailbox connected through Microsoft's
	// sign-in, and absent for one added with a password.
	Auth           string `json:"auth,omitempty"`
	NeedsReconnect bool   `json:"needs_reconnect,omitempty"`
}

// shownAccount is the nine fields of a mailbox any answer holds.
type shownAccount struct {
	ID             string  `json:"id"`
	Label          string  `json:"label"`
	Host           string  `json:"host"`
	Port           int     `json:"port"`
	UseTLS         bool    `json:"use_tls"`
	Username       string  `json:"username"`
	CreatedAt      *string `json:"created_at"`
	Auth           string  `json:"auth"`
	NeedsReconnect bool    `json:"needs_reconnect"`
}

func (s stored) shown() shownAccount {
	out := shownAccount{ID: s.ID, Label: s.Label, Host: s.Host, Port: s.Port, UseTLS: true,
		Username: s.Username, Auth: "password", NeedsReconnect: s.NeedsReconnect}
	if s.UseTLS != nil {
		out.UseTLS = *s.UseTLS
	}
	if s.CreatedAt != "" {
		out.CreatedAt = &s.CreatedAt
	}
	if s.Auth != "" {
		out.Auth = s.Auth
	}
	return out
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
	c.caller = caller
	if person && !caller.IsSession() {
		r.Errors.Write(c.response, http.StatusForbidden, sessionRequired, c.requestID)
		return c, false
	}
	return c, true
}

// failed is the sanitised 500: the reason goes to the log.
func (r Routes) failed(c call, reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", c.requestID), slog.String("reason", reason)}
	if cause != nil {
		// A driver's error or this package's own; neither quotes a record.
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(c.ctx.Context(), slog.LevelError, "a mailbox route failed", attributes...)
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
	records, err := r.Rows.Mailboxes(ctx.Context(), c.caller.UserID)
	if err != nil {
		r.failed(c, "could not read the mailboxes", err)
		return
	}
	shown := make([]shownAccount, 0, len(records))
	for _, record := range records {
		var held stored
		// An entry that is not a mailbox record is not a mailbox; it is
		// left out and the rest of the list is still shown.
		if json.Unmarshal(record, &held) == nil && held.ID != "" {
			shown = append(shown, held.shown())
		}
	}
	r.send(c, http.StatusOK, shown)
}

// form is what the add form and the unsaved test both send.
type form struct {
	label, host, username, password string
	port                            int
	useTLS                          bool
}

// readForm validates the body. The limits are the columns' and the
// protocol's: a port is 1 to 65535, an address at most 320 characters.
func readForm(c call) (form, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(c.ctx.Header("Content-Type"), requestbody.Buffered(c.ctx.Context()))
	if err != nil {
		return form{}, nil, err
	}
	if failure != nil {
		return form{}, []errorenvelope.Failure{*failure}, nil
	}
	model := requestbody.NewModel(object)
	read := form{
		label:    strings.TrimSpace(model.TextOr("label", "", 0, 200)),
		host:     strings.TrimSpace(model.Text("host", 1, 255)),
		port:     993,
		useTLS:   model.Truth("use_tls", true),
		username: strings.TrimSpace(model.Text("username", 1, 320)),
		password: strings.TrimSpace(model.Text("app_password", 1, 2000)),
	}
	if port := model.OptionalNumber("port"); port.Value != nil {
		read.port = int(*port.Value)
		if port.OutOfRange || *port.Value < 1 || *port.Value > 65535 {
			return form{}, append(model.Failures(), bodyFailure("Port must be between 1 and 65535", "port")), nil
		}
	}
	failures := model.Failures()
	if len(failures) == 0 {
		for name, text := range map[string]string{"host": read.host, "username": read.username, "app_password": read.password} {
			if text == "" {
				failures = append(failures, bodyFailure("This field must not be blank", name))
			}
		}
	}
	return read, failures, nil
}

func bodyFailure(message string, path ...string) errorenvelope.Failure {
	return errorenvelope.Failure{Location: append([]string{requestbody.BodyRoot}, path...), Message: message}
}

// unstorable reports text the config column takes and cannot give back:
// U+0000, which no jsonb value may hold.
func unstorable(texts ...string) bool {
	for _, text := range texts {
		if strings.ContainsRune(text, 0) {
			return true
		}
	}
	return false
}

// newAccountID is a random UUID (version 4), the form a mailbox's id has
// always had.
func newAccountID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	raw[6] = raw[6]&0x0f | 0x40
	raw[8] = raw[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16]), nil
}

func (r Routes) add(ctx huma.Context) {
	c, ok := r.begin(ctx, true)
	if !ok {
		return
	}
	read, problems, err := readForm(c)
	if err != nil {
		r.failed(c, "a body reached the route that was never buffered", err)
		return
	}
	if len(problems) > 0 {
		r.Errors.WriteValidation(c.response, c.requestID, problems)
		return
	}
	if unstorable(read.label, read.host, read.username) {
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, settingsUnstored, c.requestID)
		return
	}
	// Checked now so the person is told at the form; checked again, and
	// pinned, at every connection (imap.go).
	if !r.Mail.Reachable(ctx.Context(), read.host) {
		r.Errors.Write(c.response, http.StatusBadRequest, hostRefused, c.requestID)
		return
	}
	id, err := newAccountID()
	var sealed string
	if err == nil {
		sealed, err = storedtoken.Seal(r.SecretKey, read.password, r.Now(), rand.Reader)
	}
	if err != nil {
		r.failed(c, "could not seal the mailbox's password", err)
		return
	}
	label := read.label
	if label == "" {
		label = read.username
	}
	useTLS := read.useTLS
	record := stored{ID: id, Label: label, Host: read.host, Port: read.port, UseTLS: &useTLS,
		Username: read.username, AppPassword: sealed, Encrypted: true,
		CreatedAt: r.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00")}
	encoded, err := json.Marshal(record)
	if err == nil {
		err = r.Rows.Add(ctx.Context(), c.caller.UserID, encoded)
	}
	if err != nil {
		r.failed(c, "could not store the mailbox", err)
		return
	}
	r.send(c, http.StatusCreated, record.shown())
}

func (r Routes) remove(ctx huma.Context) {
	c, ok := r.begin(ctx, false)
	if !ok {
		return
	}
	removed, err := r.Rows.Remove(ctx.Context(), c.caller.UserID, ctx.Param("account_id"))
	if err != nil {
		r.failed(c, "could not remove the mailbox", err)
		return
	}
	if !removed {
		r.Errors.Write(c.response, http.StatusNotFound, accountAbsent, c.requestID)
		return
	}
	pythonjson.SendNoContent(c.response)
}

// testResult is what both tests answer: 200 either way, because a mailbox
// that does not sign in is the test's answer and not a fault of the
// request.
type testResult struct {
	Status      string  `json:"status"`
	FolderCount *int    `json:"folder_count"`
	Message     *string `json:"message"`
}

func (r Routes) probe(c call, settings Settings) {
	folders, err := r.Mail.Folders(c.ctx.Context(), settings)
	var refused *ConnectionError
	switch {
	case err == nil:
		r.send(c, http.StatusOK, testResult{Status: "ok", FolderCount: &folders})
	case errors.As(err, &refused):
		r.send(c, http.StatusOK, testResult{Status: "error", Message: &refused.Message})
	default:
		r.failed(c, "the mailbox test failed", err)
	}
}

func (r Routes) testUnsaved(ctx huma.Context) {
	c, ok := r.begin(ctx, true)
	if !ok {
		return
	}
	read, problems, err := readForm(c)
	if err != nil {
		r.failed(c, "a body reached the route that was never buffered", err)
		return
	}
	if len(problems) > 0 {
		r.Errors.WriteValidation(c.response, c.requestID, problems)
		return
	}
	r.probe(c, Settings{Host: read.host, Port: read.port, UseTLS: read.useTLS, Username: read.username, Password: read.password})
}

func (r Routes) testSaved(ctx huma.Context) {
	c, ok := r.begin(ctx, true)
	if !ok {
		return
	}
	record, err := r.Rows.Mailbox(ctx.Context(), c.caller.UserID, ctx.Param("account_id"))
	if err != nil {
		r.failed(c, "could not read the mailbox", err)
		return
	}
	var held stored
	if record == nil || json.Unmarshal(record, &held) != nil {
		r.Errors.Write(c.response, http.StatusNotFound, accountAbsent, c.requestID)
		return
	}
	if held.Auth == "microsoft" {
		settings, err := r.Microsoft.Mailbox(ctx.Context(), c.caller.UserID, record)
		var refused *ConnectionError
		switch {
		case errors.As(err, &refused):
			// The test's answer, as a password that does not sign in is.
			r.send(c, http.StatusOK, testResult{Status: "error", Message: &refused.Message})
		case err != nil:
			r.failed(c, "could not prepare the Microsoft mailbox's sign-in", err)
		default:
			r.probe(c, settings)
		}
		return
	}
	password := ""
	if held.Encrypted && held.AppPassword != "" {
		password, err = storedtoken.Open(r.SecretKey, held.AppPassword)
	}
	if err != nil || password == "" {
		r.Errors.Write(c.response, http.StatusBadRequest, passwordUnreadable, c.requestID)
		return
	}
	useTLS := held.UseTLS == nil || *held.UseTLS
	r.probe(c, Settings{Host: held.Host, Port: held.Port, UseTLS: useTLS, Username: held.Username, Password: password})
}
