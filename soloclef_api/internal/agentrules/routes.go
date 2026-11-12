package agentrules

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// Routes are the four routes over one Store.
//
// The two that read a body (the create and the update) are buffered by the
// tier's body reader before the route runs: BodyRoutes names them.
type Routes struct {
	Store     Store
	Available Availability
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
}

// routes is the table the registration and the tier's lists are made from,
// so the two cannot name different routes.
var routes = []struct {
	id, method, path, summary string
	body                      bool
	serve                     func(Routes, *request)
}{
	{"read-agent-permission-rules", http.MethodGet, RulesPath, "The caller's agent permission rules", false, Routes.list},
	{"create-agent-permission-rule", http.MethodPost, RulesPath, "Write an agent permission rule", true, Routes.create},
	{"update-agent-permission-rule", http.MethodPatch, RulePath, "Change one agent permission rule", true, Routes.update},
	{"delete-agent-permission-rule", http.MethodDelete, RulePath, "Remove one agent permission rule", false, Routes.remove},
}

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	for _, entry := range routes {
		serve, what := entry.serve, entry.summary
		huma.Register(api, huma.Operation{
			OperationID: entry.id, Method: entry.method, Path: entry.path, Summary: entry.summary, Tags: []string{"agent-permissions"},
		}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: func(ctx huma.Context) {
				serve(r, &request{
					ctx: ctx, routes: r, response: humawriter.New(ctx),
					id: requestcontext.ID(ctx.Context()), what: what,
				})
			}}, nil
		})
	}
}

// AllRoutes is the "METHOD path" of every route, for the tier's lists of
// what it serves.
func AllRoutes() []string {
	out := make([]string, len(routes))
	for i, entry := range routes {
		out[i] = entry.method + " " + entry.path
	}
	return out
}

// BodyRoutes is the "METHOD path" of every route that reads a body, for the
// tier's body reader.
func BodyRoutes() []string {
	var out []string
	for _, entry := range routes {
		if entry.body {
			out = append(out, entry.method+" "+entry.path)
		}
	}
	return out
}

// request is one request being answered.
type request struct {
	ctx      huma.Context
	routes   Routes
	response http.ResponseWriter
	id       string
	// what is the route's own summary, for the log.
	what string
}

// refuse answers with a status and a sentence.
func (q *request) refuse(status int, message string) {
	q.routes.Errors.Write(q.response, status, message, q.id)
}

// invalid answers a request that cannot be read as asked.
func (q *request) invalid(failures []errorenvelope.Failure) {
	q.routes.Errors.WriteValidation(q.response, q.id, failures)
}

// fail logs what went wrong and answers the tier's sanitised 500.
func (q *request) fail(reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", q.id), slog.String("reason", reason), slog.String("route", q.what)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	q.routes.Logger.LogAttrs(q.ctx.Context(), slog.LevelError, "an agent permission rule route failed", attributes...)
	q.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}

// caller is who is asking, once the route may answer at all: the feature is
// switched on. It answers the request itself otherwise.
//
// The routes are mounted behind the tier's authentication, so there is
// always a caller; a 500, and never an unauthenticated read of somebody's
// rules, if the wiring is ever wrong. The account is this principal's and
// is taken from nowhere else.
func (q *request) caller() (string, bool) {
	caller, authenticated := principal.FromContext(q.ctx.Context())
	if !authenticated || caller.UserID == "" {
		q.fail("the route ran without an authenticated caller", nil)
		return "", false
	}
	if !q.routes.Available.Serves() {
		q.refuse(http.StatusServiceUnavailable, flagOff)
		return "", false
	}
	return caller.UserID, true
}

// ruleID reads the path's rule id. One that is not a whole number is the
// tier's usual 422; one no rule can have is 0, which names nothing.
func (q *request) ruleID() (id int64, failures []errorenvelope.Failure) {
	id, failure, rangeErr := requestnumber.Parse(q.ctx.Param("rule_id"), "path", "rule_id")
	if failure != nil {
		return 0, []errorenvelope.Failure{*failure}
	}
	if rangeErr != nil || id < 1 {
		return 0, nil
	}
	return id, nil
}

// send writes a rule or a list of them.
func (q *request) send(status int, value any) {
	body, err := pythonjson.Marshal(value)
	if err != nil {
		q.fail("could not render the answer", err)
		return
	}
	pythonjson.Send(q.response, status, body)
}

func (r Routes) list(q *request) {
	userID, ok := q.caller()
	if !ok {
		return
	}
	rules, err := r.Store.Rules(q.ctx.Context(), userID)
	if err != nil {
		q.fail("could not read the rules", err)
		return
	}
	q.send(http.StatusOK, BuildList(rules))
}

func (r Routes) create(q *request) {
	// The body is checked before the flag and before anything is read, so
	// a request that could never be served is told why whatever the
	// deployment's setting.
	requested, failures, threadOutOfRange, decodeErr := ReadRequest(
		q.ctx.Header("Content-Type"), requestbody.Buffered(q.ctx.Context()))
	if decodeErr != nil {
		q.fail("the body could not be read", decodeErr)
		return
	}
	if len(failures) > 0 {
		q.invalid(failures)
		return
	}
	userID, ok := q.caller()
	if !ok {
		return
	}
	permission, wellFormed := StoredPermission(requested.Permission)
	if !wellFormed {
		q.refuse(http.StatusBadRequest, permissionMalformed)
		return
	}
	if threadOutOfRange || (requested.ThreadID != nil && *requested.ThreadID < 1) {
		// A number no thread has names no thread of the caller's.
		q.refuse(http.StatusNotFound, threadAbsent)
		return
	}
	created, err := r.Store.Create(q.ctx.Context(), userID, requested, permission, StoredPattern(requested.Pattern))
	switch {
	case errors.Is(err, ErrNoThread):
		q.refuse(http.StatusNotFound, threadAbsent)
	case errors.Is(err, ErrDuplicate):
		q.refuse(http.StatusConflict, duplicateOnCreate)
	case err != nil:
		q.fail("could not write the rule", err)
	default:
		q.send(http.StatusCreated, Build(created))
	}
}

func (r Routes) update(q *request) {
	ruleID, failures := q.ruleID()
	changes, bodyFailures, decodeErr := ReadChanges(
		q.ctx.Header("Content-Type"), requestbody.Buffered(q.ctx.Context()))
	if decodeErr != nil {
		q.fail("the body could not be read", decodeErr)
		return
	}
	if failures = append(failures, bodyFailures...); len(failures) > 0 {
		q.invalid(failures)
		return
	}
	userID, ok := q.caller()
	if !ok {
		return
	}
	// A body that mentions nothing still runs the statement: it sets each
	// column to itself, and its answer is the row, or that the caller has
	// no such rule.
	updated, found, err := r.Store.Update(q.ctx.Context(), userID, ruleID, changes)
	switch {
	case errors.Is(err, ErrDuplicate):
		q.refuse(http.StatusConflict, duplicateOnUpdate)
	case err != nil:
		q.fail("could not update the rule", err)
	case !found:
		q.refuse(http.StatusNotFound, ruleAbsent)
	default:
		q.send(http.StatusOK, Build(updated))
	}
}

func (r Routes) remove(q *request) {
	ruleID, failures := q.ruleID()
	if len(failures) > 0 {
		q.invalid(failures)
		return
	}
	userID, ok := q.caller()
	if !ok {
		return
	}
	removed, err := r.Store.Delete(q.ctx.Context(), userID, ruleID)
	switch {
	case err != nil:
		q.fail("could not delete the rule", err)
	case !removed:
		q.refuse(http.StatusNotFound, ruleAbsent)
	default:
		pythonjson.SendNoContent(q.response)
	}
}
