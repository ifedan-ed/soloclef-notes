package searchconnectors

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// ServerWrites are the two writes of an MCP server's own settings.
type ServerWrites interface {
	// Create inserts the owner's connection; existing is the one already
	// holding that type and name, and then nothing was written.
	Create(ctx context.Context, ownerID string, connection NewConnection) (created, existing *Connection, err error)
	// UpdateServer edits the owner's MCP server; nil leaves a part alone. A
	// nil connection is "not the owner's, or not editable"; ErrNameTaken is
	// a rename onto another server's name.
	UpdateServer(ctx context.Context, ownerID string, connectorID int64, name *string, serverConfig []byte) (*Connection, error)
}

// nameLimit is the name column's width.
const nameLimit = 100

// readBody decodes the buffered body to an object. A nil object with no
// failures cannot happen; a non-nil error is a body that reached the route
// without the buffering middleware having seen it.
func readBody(ctx huma.Context) (*pythonjson.Object, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if err != nil {
		return nil, nil, err
	}
	if failure != nil {
		return nil, []errorenvelope.Failure{*failure}, nil
	}
	return object, nil, nil
}

// bodyFailure is one field's refusal, in the validation answer's shape.
func bodyFailure(message string, path ...string) errorenvelope.Failure {
	return errorenvelope.Failure{Location: append([]string{requestbody.BodyRoot}, path...), Message: message}
}

// readName reads a connection's name: text, not blank, at most the
// column's width, and storable. present is false when the body has no
// `name` (or a null one), which a create refuses and an edit reads as
// "leave it".
func readName(body *pythonjson.Object, problems *[]errorenvelope.Failure) (name string, present bool) {
	value, named := body.Get("name")
	if !named || value == nil {
		return "", false
	}
	text, isText := value.(string)
	switch {
	case !isText:
		*problems = append(*problems, bodyFailure(requestbody.NotAString, "name"))
	case strings.TrimSpace(text) == "":
		*problems = append(*problems, bodyFailure("A name is required", "name"))
	case utf8.RuneCountInString(text) > nameLimit:
		*problems = append(*problems, bodyFailure("A name is at most 100 characters", "name"))
	case cannotBeStored(text):
		*problems = append(*problems, bodyFailure(settingsUnstored, "name"))
	}
	return strings.TrimSpace(text), true
}

// ServerCreateRoute serves POST /api/v1/connectors/mcp: add a remote MCP
// server to the caller's account.
//
// Only a person in a session adds one: a server is somewhere this process
// will send a key and take tool definitions from, and that is not a
// decision for an API key or an agent. The settings are read strictly and
// the destination is checked before anything is stored
// (server_settings.go). The server's tools are not asked for here: the
// chat discovers them at the account's next turn, which the counter moved
// below makes a cold one.
//
// Changed from the Python: a second server with a name the account already
// uses is refused (409) and not quietly renamed "name (2)".
type ServerCreateRoute struct {
	Writes ServerWrites
	Policy ServerPolicy
	Tools  ToolChanges
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r ServerCreateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-mcp-connector",
		Method:        http.MethodPost,
		Path:          ServersPath,
		DefaultStatus: http.StatusCreated,
		Summary:       "Add an MCP server",
		Description: "Adds a remote MCP server (https, a public host) to the caller's account. " +
			"A session only. The answer shows the server with its key masked.",
		Tags: []string{"connectors"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ServerCreateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the MCP server could not be added"}

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
	var (
		name     string
		incoming *pythonjson.Object
	)
	if body != nil {
		var named bool
		if name, named = readName(body, &problems); !named {
			problems = append(problems, bodyFailure(requestbody.FieldRequired, "name"))
		}
		incoming, problems = readServerConfigField(body, problems, true)
	}
	if len(problems) > 0 {
		r.Errors.WriteValidation(response, requestID, problems)
		return
	}
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, sessionRequired, requestID)
		return
	}

	settings, refusal := readServerSettings(incoming)
	if refusal == "" {
		// Nothing is stored yet, so a masked value stands for nothing.
		settings, refusal = restoreMasked(settings, nil)
	}
	if refusal != "" {
		r.Errors.Write(response, http.StatusUnprocessableEntity, refusal, requestID)
		return
	}
	origin, err := r.Policy.originForNew(ctx.Context(), caller.UserID)
	if err != nil {
		failed.caught("could not read the caller's standing", err)
		return
	}
	refusal, err = r.Policy.admit(ctx.Context(), origin, settings.Address)
	if err != nil {
		failed.caught("could not check the server's destination", err)
		return
	}
	if refusal != "" {
		r.Errors.Write(response, http.StatusBadRequest, refusal, requestID)
		return
	}

	// The origin is written here, from the caller's standing, and no
	// request can name it.
	column, err := json.Marshal(struct {
		Server serverSettings `json:"server_config"`
		Origin string         `json:"server_origin"`
	}{settings, origin})
	if err != nil {
		failed.caught("could not encode the server's config", err)
		return
	}
	created, _, err := r.Writes.Create(ctx.Context(), caller.UserID, NewConnection{
		Name: name, Type: serverTypeTag, Config: column,
	})
	if err != nil && !errors.Is(err, ErrNameTaken) {
		failed.caught("could not store the MCP server", err)
		return
	}
	if created == nil {
		r.Errors.Write(response, http.StatusConflict, nameTaken, requestID)
		return
	}
	// After the insert has committed: every process's next turn for this
	// account builds its tool list again, and finds the new server.
	toolsChanged(ctx.Context(), r.Tools, r.Logger, requestID, caller.UserID)
	answerServer(failed, response, http.StatusCreated, *created)
}

// readServerConfigField takes `server_config` out of a body: an object.
// Absent or null is a failure only where required says so.
func readServerConfigField(body *pythonjson.Object, problems []errorenvelope.Failure, required bool) (*pythonjson.Object, []errorenvelope.Failure) {
	value, present := body.Get("server_config")
	if !present || value == nil {
		if required {
			problems = append(problems, bodyFailure(requestbody.FieldRequired, "server_config"))
		}
		return nil, problems
	}
	object, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil, append(problems, bodyFailure(requestbody.NotADictionary, "server_config"))
	}
	return object, problems
}

// answerServer renders an MCP server as its reads do, key masked, and
// sends it.
func answerServer(failed failures, response http.ResponseWriter, status int, server Connection) {
	masked, err := shownServer(server)
	if err != nil {
		failed.caught(rowRefused, err)
		return
	}
	body, err := renderServer(server, masked)
	if err != nil {
		failed.uncaught(bodyUnwritten, err)
		return
	}
	pythonjson.Send(response, status, body)
}

// serverEditor is the one edit of an MCP server's name and settings,
// shared by the two routes that reach it: the server's own PUT, and the
// generic connection PUT, which is the one the settings page uses.
type serverEditor struct {
	Writes ServerWrites
	Policy ServerPolicy
}

// refusedEdit is an edit answered with a status and a sentence.
type refusedEdit struct {
	status  int
	message string
}

// apply validates and stores the edit of row. incoming is the new
// `server_config`, or nil to leave the settings alone. changed says
// whether the settings were written, which is when the chat's tool cache
// must be told.
//
// The stored settings are read from the row the caller was just shown to
// own and the write is one statement that sets one key, so trusted tools
// added while the form was open are kept (queries.sql, UpdateServer).
func (e serverEditor) apply(ctx context.Context, ownerID string, row Connection, name *string, incoming *pythonjson.Object) (updated *Connection, refused *refusedEdit, err error) {
	var column []byte
	if incoming != nil {
		settings, refusal := readServerSettings(incoming)
		if refusal == "" {
			stored := storedServerOf(row)
			settings, refusal = restoreMasked(settings, &stored)
		}
		if refusal != "" {
			return nil, &refusedEdit{http.StatusUnprocessableEntity, refusal}, nil
		}
		// The origin is the row's, never the request's.
		refusal, err := e.Policy.admit(ctx, originOf(row), settings.Address)
		if err != nil {
			return nil, nil, err
		}
		if refusal != "" {
			return nil, &refusedEdit{http.StatusBadRequest, refusal}, nil
		}
		if column, err = serverColumn(settings); err != nil {
			return nil, nil, err
		}
	}
	updated, err = e.Writes.UpdateServer(ctx, ownerID, row.ID, name, column)
	if errors.Is(err, ErrNameTaken) {
		return nil, &refusedEdit{http.StatusConflict, nameTaken}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if updated == nil {
		// The row was there a moment ago: either it was removed since, or
		// its config is not an object and cannot take a key.
		return nil, &refusedEdit{http.StatusConflict, serverUneditable}, nil
	}
	return updated, nil, nil
}

// ServerUpdateRoute serves PUT /api/v1/connectors/mcp/{connector_id}: change
// a server's name, its settings, or both. A field left out is left alone.
//
// A masked value sent back stands for the stored key while the address
// still names the same host (server_settings.go, restoreMasked). New
// settings clear the "enter the key again" flag and the tool list the old
// address answered with.
type ServerUpdateRoute struct {
	Rows   Rows
	Writes ServerWrites
	Policy ServerPolicy
	Tools  ToolChanges
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r ServerUpdateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "update-mcp-connector",
		Method:      http.MethodPut,
		Path:        ServerPath,
		Summary:     "Change an MCP server",
		Description: "Changes the caller's own server: its name, its settings, or both. " +
			"A session only. A masked key sent back keeps the stored one.",
		Tags: []string{"connectors"},
	}, func(ctx context.Context, _ *struct {
		ConnectorID string `path:"connector_id" doc:"The MCP server's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ServerUpdateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the MCP server could not be changed"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.caught(noCaller, nil)
		return
	}
	problems := make([]errorenvelope.Failure, 0, 2)
	connector := readNumber(ctx.Param("connector_id"), &problems, "path", "connector_id")
	body, bodyProblems, err := readBody(ctx)
	if err != nil {
		failed.caught("a body reached the route that was never buffered", err)
		return
	}
	problems = append(problems, bodyProblems...)
	var (
		name     *string
		incoming *pythonjson.Object
	)
	if body != nil {
		if text, named := readName(body, &problems); named {
			name = &text
		}
		incoming, problems = readServerConfigField(body, problems, false)
	}
	if len(problems) > 0 {
		r.Errors.WriteValidation(response, requestID, problems)
		return
	}
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, sessionRequired, requestID)
		return
	}
	var server *Connection
	if !connector.tooWide {
		if server, err = r.Rows.Server(ctx.Context(), caller.UserID, connector.value); err != nil {
			failed.caught(rowUnread, err)
			return
		}
	}
	if server == nil {
		r.Errors.Write(response, http.StatusNotFound, serverAbsent, requestID)
		return
	}
	updated, refused, err := serverEditor{Writes: r.Writes, Policy: r.Policy}.
		apply(ctx.Context(), caller.UserID, *server, name, incoming)
	if err != nil {
		failed.caught("could not change the MCP server", err)
		return
	}
	if refused != nil {
		r.Errors.Write(response, refused.status, refused.message, requestID)
		return
	}
	// After the commit. A rename is told too: the model is shown the
	// server's name beside its tools.
	toolsChanged(ctx.Context(), r.Tools, r.Logger, requestID, caller.UserID)
	answerServer(failed, response, http.StatusOK, *updated)
}
