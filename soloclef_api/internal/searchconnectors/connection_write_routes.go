package searchconnectors

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// This file is the two generic writes: making a connection that needs no
// sign-in, and changing the settings of any connection.
//
// # The rule both keep
//
// A request may name only the config keys a read of that type shows
// (public_config.go). A read never answers a grant, a password or its
// ciphertext, so a request that names `refresh_token` or `app_password` is
// carrying something it did not get from here, and if it were merged into
// the row the server would decrypt it and act with it. It is refused out
// loud, naming the keys. Two public keys are shown and still never written
// from a request, because the server owns them: a mailbox connection's
// `accounts` (what a read shows of them is a reduction; writing it back
// would replace the stored credentials with it) and `auth_expired`.
//
// # What changed from the Python
//
//   - Only a web-pages connection is made here. A Drive, a Nextcloud, a
//     mailbox and an MCP server each hold a sign-in and are made by their
//     own flow; Python let this route make a Drive row with no grant.
//   - Whether a connection can be indexed is its type's, not the request's.
//   - The schedule's next run is the server's. A request cannot set it.
//   - Making a connection whose name the account already uses returns that
//     one (the page's quick-add is then harmless to click twice); renaming
//     onto a used name is a 409 and not a 500.

// Where each kind of connection that this route does not make is made.
var madeElsewhere = map[string]string{
	serverTypeTag:            notAServerHere,
	"NEXTCLOUD_CONNECTOR":    "Connect Nextcloud with the Connect button in Connectors",
	mailboxType:              "Add mailboxes from Connectors > Email (IMAP)",
	"GOOGLE_DRIVE_CONNECTOR": "Connect Google Drive with the Connect button in Connectors",
}

// indexable is the types whose content is brought in by an indexing run,
// and so the only ones that can have a schedule.
var indexable = map[string]bool{"WEBCRAWLER_CONNECTOR": true, "GOOGLE_DRIVE_CONNECTOR": true}

// serverOwnedKeys are public keys a request may send back and that are
// dropped rather than written.
var serverOwnedKeys = map[string]bool{"accounts": true, "auth_expired": true}

// mostMinutes bounds a schedule's pace at a year, which keeps the next
// run's arithmetic far from any overflow.
const mostMinutes = 366 * 24 * 60

const (
	typeUnchangeable   = "A connection's type cannot be changed"
	notIndexable       = "periodic_indexing_enabled can only be True for indexable connectors"
	frequencyRequired  = "indexing_frequency_minutes is required when periodic_indexing_enabled is True"
	frequencyRange     = "indexing_frequency_minutes must be between 1 and 527040"
	connectionUnedited = "This connection's stored settings cannot be edited. Remove it and connect again."
)

// ConnectionWrites are the generic writes' statements.
type ConnectionWrites interface {
	ServerWrites
	// UpdateConnection edits the owner's connection that is not an MCP
	// server. Nil is "not the owner's, or not editable"; ErrNameTaken is a
	// rename onto another connection's name.
	UpdateConnection(ctx context.Context, ownerID string, connectorID int64, edit ConnectionEdit) (*Connection, error)
}

// writableSettings sifts a request's `config` for a connection of this
// type: the settings to merge into the stored config, or the refusal.
// status is 0 when it is accepted.
func writableSettings(connectionType string, config *pythonjson.Object) (settings *pythonjson.Object, status int, refusal string) {
	if holdsUnstorable(config, 0) {
		return nil, http.StatusUnprocessableEntity, settingsUnstored
	}
	allowed := publicKeys[connectionType]
	var refused []string
	settings = pythonjson.NewObject()
	for _, key := range config.Keys() {
		switch {
		case !allowed[key]:
			refused = append(refused, printable(key))
		case serverOwnedKeys[key]:
		default:
			value, _ := config.Get(key)
			settings.Set(key, value)
		}
	}
	if len(refused) > 0 {
		// Sorted, so the refusal reads the same whatever order the request
		// used.
		slices.Sort(refused)
		return nil, http.StatusBadRequest, "These settings cannot be changed here: " + strings.Join(refused, ", ") +
			". Connect again from Connectors to change a sign-in"
	}
	return settings, 0, ""
}

// holdsUnstorable reports whether any key or text anywhere in a value is
// one the column cannot give back (cannotBeStored). The depth is bounded
// by the body's own nesting limit; the level is only a guard.
func holdsUnstorable(value any, level int) bool {
	if level > 64 {
		return true
	}
	switch held := value.(type) {
	case string:
		return cannotBeStored(held)
	case []any:
		for _, item := range held {
			if holdsUnstorable(item, level+1) {
				return true
			}
		}
	case *pythonjson.Object:
		for _, key := range held.Keys() {
			item, _ := held.Get(key)
			if cannotBeStored(key) || holdsUnstorable(item, level+1) {
				return true
			}
		}
	}
	return false
}

// schedule is the three columns of a connection's indexing schedule.
type schedule struct {
	enabled bool
	minutes *int64
	next    *time.Time
}

// scheduled decides a schedule from what is asked for, or refuses it.
// restart says the next run is to be counted from now: the schedule was
// just switched on or its pace changed. Otherwise the run already planned
// stands.
func scheduled(canIndex, enabled bool, minutes *int64, planned *time.Time, restart bool, now time.Time) (schedule, string) {
	if minutes != nil && (*minutes < 1 || *minutes > mostMinutes) {
		return schedule{}, frequencyRange
	}
	if !enabled {
		// Off: the pace is kept for when it is switched on again; no run
		// is planned.
		return schedule{minutes: minutes}, ""
	}
	if !canIndex {
		return schedule{}, notIndexable
	}
	if minutes == nil {
		return schedule{}, frequencyRequired
	}
	if restart || planned == nil {
		next := now.Add(time.Duration(*minutes) * time.Minute)
		planned = &next
	}
	return schedule{enabled: true, minutes: minutes, next: planned}, ""
}

// ConnectionCreateRoute serves POST /api/v1/search-source-connectors: make
// a web-pages connection. Every other type is made by its own flow and is
// refused here with where to go.
type ConnectionCreateRoute struct {
	Writes ConnectionWrites
	Now    func() time.Time
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r ConnectionCreateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-search-source-connector",
		Method:      http.MethodPost,
		Path:        ListPath,
		Summary:     "Make a connection",
		Description: "Makes a web-pages connection for the caller. A connection that holds a " +
			"sign-in (Drive, Nextcloud, a mailbox, an MCP server) is made by its own flow.",
		Tags: []string{"connectors"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ConnectionCreateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the connection could not be made"}

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
		name, connectionType string
		config               = pythonjson.NewObject()
		vision, periodic     bool
		minutes              *int64
	)
	if body != nil {
		var named bool
		if name, named = readName(body, &problems); !named {
			problems = append(problems, bodyFailure(requestbody.FieldRequired, "name"))
		}
		model := requestbody.NewModel(body)
		connectionType = model.Text("connector_type", 0, 0)
		if settings := model.MaybeMapping("config"); settings.Value != nil {
			config = settings.Value
		}
		vision = model.Truth("enable_vision_llm", false)
		periodic = model.Truth("periodic_indexing_enabled", false)
		minutes = model.OptionalNumber("indexing_frequency_minutes").Value
		problems = append(problems, model.Failures()...)
	}
	if len(problems) > 0 {
		r.Errors.WriteValidation(response, requestID, problems)
		return
	}
	if _, isServer := config.Get("server_config"); isServer {
		r.Errors.Write(response, http.StatusBadRequest, notAServerHere, requestID)
		return
	}
	if elsewhere, refused := madeElsewhere[connectionType]; refused {
		r.Errors.Write(response, http.StatusBadRequest, elsewhere, requestID)
		return
	}
	if _, known := publicKeys[connectionType]; !known {
		r.Errors.WriteValidation(response, requestID,
			[]errorenvelope.Failure{bodyFailure("This is not a kind of connection", "connector_type")})
		return
	}
	settings, status, refusal := writableSettings(connectionType, config)
	if status != 0 {
		r.Errors.Write(response, status, refusal, requestID)
		return
	}
	plan, refusal := scheduled(indexable[connectionType], periodic, minutes, nil, true, r.Now())
	if refusal != "" {
		r.Errors.Write(response, http.StatusUnprocessableEntity, refusal, requestID)
		return
	}
	column, err := pythonjson.Marshal(settings)
	if err != nil {
		failed.caught("could not encode the connection's settings", err)
		return
	}
	created, existing, err := r.Writes.Create(ctx.Context(), caller.UserID, NewConnection{
		Name: name, Type: connectionType, IsIndexable: indexable[connectionType], Config: column,
		EnableVisionLLM: vision, PeriodicIndexingEnabled: plan.enabled,
		IndexingFrequencyMinutes: plan.minutes, NextScheduledAt: plan.next,
	})
	if errors.Is(err, ErrNameTaken) {
		r.Errors.Write(response, http.StatusConflict, connectionTaken, requestID)
		return
	}
	if err != nil {
		failed.caught("could not store the connection", err)
		return
	}
	if created == nil {
		// The account already has it; it is answered as it is, unchanged.
		created = existing
	}
	answerConnection(failed, response, *created)
}

// ConnectionUpdateRoute serves PUT /api/v1/search-source-connectors/{connector_id}:
// change the caller's connection. A field left out is left alone.
//
// It is also the route the settings page edits an MCP server through. For
// one, `name` and `config.server_config` are the whole edit, under the
// server's own rules (server_write_routes.go): a session only, the masked
// key restored, the destination checked. The other public keys of its
// config are accepted and ignored, because the page sends back what it
// read.
type ConnectionUpdateRoute struct {
	Rows   Rows
	Writes ConnectionWrites
	Policy ServerPolicy
	Tools  ToolChanges
	Now    func() time.Time
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r ConnectionUpdateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "update-search-source-connector",
		Method:      http.MethodPut,
		Path:        OnePath,
		Summary:     "Change a connection",
		Description: "Changes the caller's own connection: its name, the settings a read shows, " +
			"the vision switch and the indexing schedule. A sign-in is never changed here.",
		Tags: []string{"connectors"},
	}, func(ctx context.Context, _ *struct {
		ConnectorID string `path:"connector_id" doc:"The connection's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ConnectionUpdateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the connection could not be changed"}

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
		name           *string
		connectionType requestbody.MaybeText
		config         requestbody.MaybeMapping
		vision         requestbody.MaybeTruth
		periodic       requestbody.MaybeTruth
		minutes        requestbody.OptionalNumber
		minutesNamed   bool
	)
	if body != nil {
		if text, named := readName(body, &problems); named {
			name = &text
		}
		model := requestbody.NewModel(body)
		connectionType = model.MaybeText("connector_type", 0, 0)
		config = model.MaybeMapping("config")
		vision = model.MaybeTruth("enable_vision_llm")
		periodic = model.MaybeTruth("periodic_indexing_enabled")
		minutes = model.OptionalNumber("indexing_frequency_minutes")
		_, minutesNamed = body.Get("indexing_frequency_minutes")
		problems = append(problems, model.Failures()...)
	}
	if len(problems) > 0 {
		r.Errors.WriteValidation(response, requestID, problems)
		return
	}

	var row *Connection
	if !connector.tooWide {
		if row, err = r.Rows.Connection(ctx.Context(), caller.UserID, connector.value); err != nil {
			failed.caught(rowUnread, err)
			return
		}
	}
	if row == nil {
		r.Errors.Write(response, http.StatusNotFound, connectorAbsent, requestID)
		return
	}
	if connectionType.Value != nil && *connectionType.Value != row.Type {
		r.Errors.Write(response, http.StatusBadRequest, typeUnchangeable, requestID)
		return
	}
	var settings *pythonjson.Object
	if config.Value != nil {
		if _, names := config.Value.Get("server_config"); names && !row.IsServer() {
			r.Errors.Write(response, http.StatusBadRequest, notAServerHere, requestID)
			return
		}
		var status int
		var refusal string
		if settings, status, refusal = writableSettings(row.Type, config.Value); status != 0 {
			r.Errors.Write(response, status, refusal, requestID)
			return
		}
	}

	if row.IsServer() {
		r.serveServer(ctx, failed, caller, *row, name, settings, periodic)
		return
	}

	// The schedule as it would be after this edit.
	enabled := row.PeriodicIndexingEnabled
	if periodic.Value != nil {
		enabled = *periodic.Value
	}
	pace := row.IndexingFrequencyMinutes
	if minutesNamed {
		pace = minutes.Value
	}
	restart := (periodic.Value != nil && *periodic.Value && !row.PeriodicIndexingEnabled) ||
		(minutesNamed && !sameMinutes(pace, row.IndexingFrequencyMinutes))
	plan, refusal := scheduled(row.IsIndexable, enabled, pace, row.NextScheduledAt, restart, r.Now())
	if refusal != "" {
		r.Errors.Write(response, http.StatusUnprocessableEntity, refusal, requestID)
		return
	}
	edit := ConnectionEdit{Name: name, EnableVisionLLM: vision.Value,
		PeriodicIndexingEnabled: plan.enabled, IndexingFrequencyMinutes: plan.minutes, NextScheduledAt: plan.next}
	if settings != nil && settings.Len() > 0 {
		if edit.Settings, err = pythonjson.Marshal(settings); err != nil {
			failed.caught("could not encode the connection's settings", err)
			return
		}
	}
	updated, err := r.Writes.UpdateConnection(ctx.Context(), caller.UserID, row.ID, edit)
	if errors.Is(err, ErrNameTaken) {
		r.Errors.Write(response, http.StatusConflict, connectionTaken, requestID)
		return
	}
	if err != nil {
		failed.caught("could not change the connection", err)
		return
	}
	if updated == nil {
		r.Errors.Write(response, http.StatusConflict, connectionUnedited, requestID)
		return
	}
	answerConnection(failed, response, *updated)
}

// serveServer is the edit of an MCP server that came through this route.
func (r ConnectionUpdateRoute) serveServer(ctx huma.Context, failed failures, caller principal.Principal,
	row Connection, name *string, settings *pythonjson.Object, periodic requestbody.MaybeTruth) {
	if !caller.IsSession() {
		r.Errors.Write(failed.response, http.StatusForbidden, sessionRequired, failed.requestID)
		return
	}
	if periodic.Value != nil && *periodic.Value {
		r.Errors.Write(failed.response, http.StatusUnprocessableEntity, notIndexable, failed.requestID)
		return
	}
	var incoming *pythonjson.Object
	if settings != nil {
		if value, named := settings.Get("server_config"); named {
			object, isObject := value.(*pythonjson.Object)
			if !isObject {
				r.Errors.Write(failed.response, http.StatusUnprocessableEntity, settingsNotObject, failed.requestID)
				return
			}
			incoming = object
		}
	}
	if name == nil && incoming == nil {
		// Nothing of a server that this route edits was named.
		answerConnection(failed, failed.response, row)
		return
	}
	updated, refused, err := serverEditor{Writes: r.Writes, Policy: r.Policy}.
		apply(ctx.Context(), caller.UserID, row, name, incoming)
	if err != nil {
		failed.caught("could not change the MCP server", err)
		return
	}
	if refused != nil {
		r.Errors.Write(failed.response, refused.status, refused.message, failed.requestID)
		return
	}
	toolsChanged(ctx.Context(), r.Tools, r.Logger, failed.requestID, caller.UserID)
	answerConnection(failed, failed.response, *updated)
}

func sameMinutes(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
