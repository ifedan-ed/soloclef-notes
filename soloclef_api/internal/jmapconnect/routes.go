package jmapconnect

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapaccounts"
)

// Mailboxes is what the routes do with an account's JMAP mailboxes
// (*jmapaccounts.Mail in a deployment, a stand-in in the tests).
type Mailboxes interface {
	Accounts(ctx context.Context, ownerID string) ([]jmapaccounts.Account, error)
	Test(ctx context.Context, ownerID, accountID string) (jmapaccounts.Probe, error)
	ConnectAppPassword(ctx context.Context, ownerID, label, serverURL, username, password string) (jmapaccounts.Account, error)
	ConnectGrant(ctx context.Context, ownerID, serverURL, clientID string, metadata jmapaccounts.Metadata, grant jmapaccounts.Grant) (jmapaccounts.Account, error)
	Disconnect(ctx context.Context, ownerID, accountID string) (bool, error)
}

// SignInServer is the requests made to a server's OAuth endpoints
// (jmapaccounts.OAuth).
type SignInServer interface {
	Discover(ctx context.Context, serverURL string) (jmapaccounts.Metadata, error)
	Preflight(ctx context.Context, serverURL, authorizationURL string) error
	Exchange(ctx context.Context, tokenEndpoint, clientID, redirectURI, code, verifier string, now time.Time) (jmapaccounts.Grant, error)
}

// Routes serves the eight routes. Every field is required.
type Routes struct {
	Mailboxes Mailboxes
	OAuth     SignInServer
	Settings  Settings
	Now       func() time.Time
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (r Routes) Check() error {
	if r.Mailboxes == nil || r.OAuth == nil || r.Now == nil || r.Logger == nil {
		return errors.New("jmapconnect: Mailboxes, OAuth, Now and Logger are all required")
	}
	return r.Settings.Check()
}

// Register adds the eight operations.
func (r Routes) Register(api huma.API) {
	type account struct {
		AccountID string `path:"account_id" doc:"The mailbox's id"`
	}
	type callback struct {
		Code  string `query:"code"`
		Error string `query:"error"`
		State string `query:"state"`
		Iss   string `query:"iss"`
	}
	stream := func(serve func(huma.Context)) *huma.StreamResponse { return &huma.StreamResponse{Body: serve} }
	operation := func(id, method, path, summary, description string, status int) huma.Operation {
		return huma.Operation{OperationID: id, Method: method, Path: path, Summary: summary, Description: description,
			DefaultStatus: status, Tags: []string{"connectors"}}
	}
	huma.Register(api, operation("list-jmap-accounts", http.MethodGet, AccountsPath, "The caller's JMAP mailboxes",
		"Oldest first. Never a password or a token.", 0),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.list), nil })
	huma.Register(api, operation("add-jmap-account", http.MethodPost, AccountsPath, "Connect a JMAP mailbox with an app password",
		"Signs in first; stores the app password sealed only if it works. A session only.", http.StatusCreated),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.addAppPassword), nil })
	huma.Register(api, operation("delete-jmap-account", http.MethodDelete, AccountPath, "Remove a JMAP mailbox",
		"Removes the mailbox and its drafts, and revokes its sign-in at the server where it can.", 0),
		func(context.Context, *account) (*huma.StreamResponse, error) { return stream(r.remove), nil })
	huma.Register(api, operation("test-jmap-account", http.MethodPost, AccountTestPath, "Test a JMAP mailbox",
		"Signs in, counts the folders and says whether the sign-in may send. A session only.", 0),
		func(context.Context, *account) (*huma.StreamResponse, error) { return stream(r.test), nil })
	huma.Register(api, operation("jmap-oauth-availability", http.MethodGet, AvailabilityPath, "Whether Stalwart sign-in is offered",
		"Whether this deployment has Stalwart's sign-in set up. Only that.", 0),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.availability), nil })
	huma.Register(api, operation("jmap-oauth-connect", http.MethodPost, ConnectPath, "Start a Stalwart sign-in",
		"Answers the server's sign-in page. The attempt is bound to this browser by a cookie. A session only.", 0),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.connect), nil })
	huma.Register(api, operation("jmap-oauth-callback", http.MethodGet, CallbackPath, "The server's sign-in callback",
		"Where the server sends the person back. Saves the mailbox for the browser that started the sign-in.", 0),
		func(context.Context, *callback) (*huma.StreamResponse, error) { return stream(r.callback), nil })
	huma.Register(api, operation("jmap-oauth-client-document", http.MethodGet, ClientDocumentPath, "This app's OAuth client document",
		"The client metadata document a Stalwart server reads this app's redirect URI from.", 0),
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.clientDocument), nil })
}

// call is one request's common parts.
type call struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	caller    principal.Principal
}

// begin answers a request with no caller (a wiring fault) and, where
// person is true, one that is not a person in a session: connecting a
// mailbox stores a credential and makes this process connect somewhere,
// which an API key may not ask for.
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
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(c.ctx.Context(), slog.LevelError, "a JMAP mailbox route failed", attributes...)
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
