package nextcloudconnect

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/storedtoken"
)

// Routes serves the five routes. In a deployment that names no Nextcloud
// server Credentials and API are nil: the status route then says so
// (`configured: false`) and the sign-in routes answer 503.
type Routes struct {
	Rows Rows
	// Credentials reads a stored connection, and holds the one server a
	// connection may be for and the key its password is sealed with.
	Credentials *nextcloud.Credentials
	API         *LoginAPI
	Pending     Pending
	Now         func() time.Time
	Errors      errorenvelope.Writer
	Logger      *slog.Logger
}

// Check reports what a configured deployment is missing. Called once at
// start-up.
func (r Routes) Check() error {
	switch {
	case r.Rows == nil || r.Now == nil || r.Logger == nil:
		return errors.New("nextcloudconnect: Rows, Now and Logger are required")
	case (r.Credentials == nil) != (r.API == nil):
		return errors.New("nextcloudconnect: Credentials and API are set together, or neither")
	case r.Credentials != nil && (r.Pending.Store == nil || r.Pending.SecretKey == "" || r.Pending.Now == nil):
		return errors.New("nextcloudconnect: a configured deployment needs the pending sign-ins' store, key and clock")
	}
	return nil
}

// Register adds the five operations.
func (r Routes) Register(api huma.API) {
	type flow struct {
		FlowID string `path:"flow_id" doc:"The sign-in's id, from the start"`
	}
	type connector struct {
		ConnectorID string `path:"connector_id" doc:"The connection's id"`
	}
	stream := func(serve func(huma.Context)) *huma.StreamResponse { return &huma.StreamResponse{Body: serve} }
	huma.Register(api, huma.Operation{
		OperationID: "nextcloud-connection", Method: http.MethodGet, Path: ConnectionPath,
		Summary:     "The caller's Nextcloud connection",
		Description: "Whether this deployment has a Nextcloud, which server it is, and the caller's connection to it if any.",
		Tags:        []string{"connectors"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.status), nil })
	huma.Register(api, huma.Operation{
		OperationID: "nextcloud-login-start", Method: http.MethodPost, Path: LoginPath,
		Summary:     "Start a Nextcloud sign-in",
		Description: "Asks Nextcloud for a login page. The answer is the page to open and an id to poll with.",
		Tags:        []string{"connectors"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.start), nil })
	huma.Register(api, huma.Operation{
		OperationID: "nextcloud-login-poll", Method: http.MethodPost, Path: PollPath,
		Summary:     "Poll a Nextcloud sign-in",
		Description: "Asks Nextcloud once whether the person has granted access, and saves the connection if so.",
		Tags:        []string{"connectors"},
	}, func(context.Context, *flow) (*huma.StreamResponse, error) { return stream(r.poll), nil })
	huma.Register(api, huma.Operation{
		OperationID: "nextcloud-login-cancel", Method: http.MethodDelete, Path: CancelPath,
		DefaultStatus: http.StatusNoContent,
		Summary:       "Give a Nextcloud sign-in up",
		Description:   "Forgets a sign-in the person abandoned. Nextcloud drops its half after twenty minutes.",
		Tags:          []string{"connectors"},
	}, func(context.Context, *flow) (*huma.StreamResponse, error) { return stream(r.cancel), nil })
	huma.Register(api, huma.Operation{
		OperationID: "nextcloud-disconnect", Method: http.MethodDelete, Path: DisconnectPath,
		Summary:     "Disconnect Nextcloud",
		Description: "Removes the caller's connection and ends its app password at Nextcloud. Imported files stay.",
		Tags:        []string{"connectors"},
	}, func(context.Context, *connector) (*huma.StreamResponse, error) { return stream(r.disconnect), nil })
}

// The answers' shapes, which the web app reads field by field.
type accountView struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name"`
}

type connectionView struct {
	// Configured is false when the deployment names no Nextcloud server.
	Configured bool `json:"configured"`
	// Server is the server people connect to, as a label: host and
	// sub-path, no scheme.
	Server      *string      `json:"server"`
	ConnectorID *int64       `json:"connector_id"`
	Account     *accountView `json:"account"`
	// NeedsReconnect: the stored sign-in cannot be used (made for another
	// server, incomplete, or sealed with another key).
	NeedsReconnect bool `json:"needs_reconnect"`
}

// view is the caller's connection as any answer shows it. Nothing of the
// stored password, sealed or not, is in it.
func (r Routes) view(held *connection) connectionView {
	shown := connectionView{}
	if r.Credentials != nil {
		shown.Configured = true
		label := r.Credentials.ServerURL
		if parsed, err := url.Parse(label); err == nil {
			label = parsed.Host + parsed.Path
		}
		shown.Server = &label
	}
	if held == nil {
		return shown
	}
	shown.ConnectorID = &held.ID
	var stored struct {
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
	}
	_ = json.Unmarshal(held.Config, &stored)
	if stored.DisplayName == "" {
		stored.DisplayName = stored.Username
	}
	shown.Account = &accountView{UserID: stored.Username, DisplayName: stored.DisplayName}
	if r.Credentials == nil {
		shown.NeedsReconnect = true
	} else if _, err := r.Credentials.Account(held.Config); err != nil {
		shown.NeedsReconnect = true
	}
	return shown
}

// call is one request's common parts.
type call struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	caller    principal.Principal
}

// begin answers the two refusals every route shares and returns ok false
// when it has: no caller (a wiring fault, 500), and a caller that is not a
// person in a session (403).
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
	r.Logger.LogAttrs(c.ctx.Context(), slog.LevelError, "a Nextcloud connection route failed", attributes...)
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

// unconfigured answers the 503 of a deployment with no Nextcloud and
// reports whether it did.
func (r Routes) unconfigured(c call) bool {
	if r.Credentials != nil {
		return false
	}
	r.Errors.WriteCoded(c.response, http.StatusServiceUnavailable, notConfiguredCode, notConfigured, c.requestID)
	return true
}

// nextcloudFailed answers a failure of Nextcloud's own: 502 with a
// sentence that is safe to show, or the 500 for anything else.
func (r Routes) nextcloudFailed(c call, code string, err error) {
	var shown *LoginError
	if errors.As(err, &shown) {
		r.Errors.WriteCoded(c.response, http.StatusBadGateway, code, shown.Message, c.requestID)
		return
	}
	r.failed(c, "a request to Nextcloud failed", err)
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
	r.send(c, http.StatusOK, r.view(held))
}

func (r Routes) start(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok || r.unconfigured(c) {
		return
	}
	opened, err := r.API.start(ctx.Context())
	if err != nil {
		r.nextcloudFailed(c, unreachableCode, err)
		return
	}
	flowID, err := newFlowID()
	if err == nil {
		err = r.Pending.add(ctx.Context(), flowID, pendingLogin{
			UserID: c.caller.UserID, PollEndpoint: opened.PollEndpoint,
			PollToken: opened.PollToken, LoginURL: opened.LoginURL,
		})
	}
	if err != nil {
		r.failed(c, "could not keep the pending sign-in", err)
		return
	}
	r.send(c, http.StatusOK, struct {
		FlowID    string `json:"flow_id"`
		LoginURL  string `json:"login_url"`
		ExpiresIn int    `json:"expires_in"`
	}{flowID, opened.LoginURL, int(flowLifetime / time.Second)})
}

// flowOf is the caller's pending sign-in. Another account's, an expired
// one and one that never was are the same nil, and the same 404.
func (r Routes) flowOf(c call) (string, *pendingLogin, error) {
	flowID := c.ctx.Param("flow_id")
	flow, err := r.Pending.get(c.ctx.Context(), flowID)
	if err != nil || flow == nil || flow.UserID != c.caller.UserID {
		return flowID, nil, err
	}
	return flowID, flow, nil
}

type progress struct {
	Status     string          `json:"status"`
	Connection *connectionView `json:"connection"`
}

func (r Routes) poll(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok || r.unconfigured(c) {
		return
	}
	flowID, flow, err := r.flowOf(c)
	if err != nil {
		r.failed(c, "could not read the pending sign-in", err)
		return
	}
	if flow == nil {
		r.Errors.WriteCoded(c.response, http.StatusNotFound, flowGoneCode, flowGone, c.requestID)
		return
	}
	login, err := r.API.poll(ctx.Context(), flow.PollEndpoint, flow.PollToken)
	if err != nil {
		r.nextcloudFailed(c, unreachableCode, err)
		return
	}
	if login == nil {
		r.send(c, http.StatusOK, progress{Status: "pending"})
		return
	}
	// Nextcloud hands the password over once: from here the flow is spent,
	// and the password is either saved or ended. Nothing below may return
	// leaving it alive and unrecorded, so the rest runs to its end even if
	// the browser has gone.
	settle := context.WithoutCancel(ctx.Context())
	if err := r.Pending.remove(settle, flowID); err != nil {
		r.Logger.LogAttrs(settle, slog.LevelWarn, "a finished Nextcloud sign-in could not be forgotten; it expires on its own",
			slog.String("request_id", c.requestID), slog.String("error", err.Error()))
	}
	who, err := r.API.identity(settle, *login)
	if err == nil {
		err = r.API.listsFiles(settle, who.UserID, *login)
	}
	if err != nil {
		r.API.revoke(settle, *login)
		r.nextcloudFailed(c, unusableCode, err)
		return
	}
	sealed, err := storedtoken.Seal(r.Credentials.SecretKey, login.AppPassword, r.Now(), rand.Reader)
	var config []byte
	if err == nil {
		// The stored form every reader of a Nextcloud row expects
		// (nextcloud.Credentials.Account), the Python tier's.
		config, err = json.Marshal(map[string]any{
			"server_url":       r.Credentials.ServerURL,
			"username":         who.UserID,
			"login_name":       login.LoginName,
			"display_name":     who.DisplayName,
			"auth_method":      "login_flow",
			"app_password":     sealed,
			"_token_encrypted": true,
		})
	}
	var (
		id       int64
		previous []byte
	)
	if err == nil {
		id, previous, err = r.Rows.Save(settle, c.caller.UserID, config)
	}
	if err != nil {
		// Not saved, so nothing will ever use or end it: end it now.
		r.API.revoke(settle, *login)
		r.failed(c, "could not save the Nextcloud connection", err)
		return
	}
	r.Logger.LogAttrs(settle, slog.LevelInfo, "Nextcloud connected",
		slog.String("user_id", c.caller.UserID), slog.Int64("connector_id", id))
	if previous != nil {
		// The replaced app password is of no further use; retire it.
		r.revokeStored(settle, previous)
	}
	shown := r.view(&connection{ID: id, Config: config})
	r.send(c, http.StatusOK, progress{Status: "connected", Connection: &shown})
}

// revokeStored ends the app password a stored config holds, and reports
// whether Nextcloud confirmed it is gone. A config that cannot be used
// (another server's, incomplete, sealed with another key) is not sent
// anywhere.
func (r Routes) revokeStored(ctx context.Context, config []byte) bool {
	if r.Credentials == nil {
		return false
	}
	account, err := r.Credentials.Account(config)
	if err != nil {
		return false
	}
	return r.API.revoke(ctx, granted{LoginName: account.LoginName, AppPassword: account.AppPassword})
}

func (r Routes) cancel(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok {
		return
	}
	if r.Credentials != nil {
		flowID, flow, err := r.flowOf(c)
		if err != nil {
			r.failed(c, "could not read the pending sign-in", err)
			return
		}
		// Only the caller's own; anything else is left exactly as it is,
		// and answered the same.
		if flow != nil {
			if err := r.Pending.remove(ctx.Context(), flowID); err != nil {
				r.failed(c, "could not forget the pending sign-in", err)
				return
			}
		}
	}
	pythonjson.SendNoContent(c.response)
}

func (r Routes) disconnect(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok {
		return
	}
	connectorID, err := strconv.ParseInt(ctx.Param("connector_id"), 10, 64)
	if err != nil || connectorID < 1 {
		// Not an id any connection has.
		r.Errors.Write(c.response, http.StatusNotFound, connectionAbsent, c.requestID)
		return
	}
	config, found, err := r.Rows.Delete(ctx.Context(), c.caller.UserID, connectorID)
	if err != nil {
		r.failed(c, "could not remove the connection", err)
		return
	}
	if !found {
		r.Errors.Write(c.response, http.StatusNotFound, connectionAbsent, c.requestID)
		return
	}
	// After the row is gone, and whether or not the browser still waits:
	// a removed connection should be dead at Nextcloud too.
	revoked := r.revokeStored(context.WithoutCancel(ctx.Context()), config)
	r.send(c, http.StatusOK, struct {
		Revoked bool `json:"revoked"`
	}{revoked})
}
