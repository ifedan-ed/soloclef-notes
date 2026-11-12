package automations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"git.danvics.com/danvics/soloclef/soloclef_automations/definition"
	"git.danvics.com/danvics/soloclef/soloclef_automations/launch"
	"git.danvics.com/danvics/soloclef/soloclef_automations/runqueue"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Enqueuer publishes a run's message (celeryqueue.Queue).
type Enqueuer interface {
	Enqueue(ctx context.Context, message produce.Message) (string, error)
}

// ModelMenu is the chat's Models: which model would answer an account
// now (a run that names no model is answered by that one), and which of
// the names a definition gives the account's menu does not offer (the
// proxy's list and the account's own models).
type ModelMenu interface {
	Look(ctx context.Context, userID string) (assembly.Chosen, error)
	NotOffered(ctx context.Context, userID string, names []string) ([]string, error)
}

// Routes serves the package's routes. Every field but SendsMail is
// required.
type Routes struct {
	Pool   *pgxpool.Pool
	Queue  Enqueuer
	Models ModelMenu
	// SendsMail says this deployment sends mail (SMTP_ENABLED): an
	// automation may then ask for its result to be emailed. Where it does
	// not, such an automation is refused when it is saved, in a sentence.
	SendsMail bool
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
	Now       func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (r Routes) Check() error {
	if r.Pool == nil || r.Queue == nil || r.Models == nil || r.Logger == nil || r.Now == nil {
		return errors.New("automations: a dependency is missing")
	}
	return nil
}

func (r Routes) Register(api huma.API) {
	for _, route := range []struct {
		method, path, id, summary string
		serve                     func(*exchange)
	}{
		{http.MethodGet, ListPath, "list-automations", "The account's automations", r.list},
		{http.MethodPost, ListPath, "create-automation", "Save an automation, with its first triggers", r.create},
		{http.MethodGet, EligibilityPath, "automation-model-eligibility", "Whether the account's model can run an automation", r.eligibility},
		{http.MethodGet, OnePath, "get-automation", "One automation with its triggers", r.read},
		{http.MethodPatch, OnePath, "update-automation", "Change an automation; pausing stops its runs", r.update},
		{http.MethodDelete, OnePath, "delete-automation", "Delete an automation with its triggers and runs", r.remove},
		{http.MethodPost, RunNowPath, "run-automation-now", "Start a run by hand", r.runNow},
		{http.MethodPost, TriggersPath, "add-automation-trigger", "Add a trigger", r.addTrigger},
		{http.MethodPatch, TriggerPath, "update-automation-trigger", "Change or switch a trigger", r.updateTrigger},
		{http.MethodDelete, TriggerPath, "delete-automation-trigger", "Remove a trigger", r.removeTrigger},
		{http.MethodGet, RunsPath, "list-automation-runs", "An automation's runs, newest first", r.listRuns},
		{http.MethodGet, RunPath, "get-automation-run", "One run with its transcript and result", r.readRun},
		{http.MethodPost, CancelPath, "cancel-automation-run", "Cancel a run that has not finished", r.cancelRun},
	} {
		serve := route.serve
		huma.Register(api, huma.Operation{
			OperationID: route.id, Method: route.method, Path: route.path, Summary: route.summary, Tags: []string{"automations"},
		}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: func(ctx huma.Context) {
				x := &exchange{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), routes: r}
				caller, ok := principal.FromContext(ctx.Context())
				if !ok {
					x.fail("the route ran without a caller", nil)
					return
				}
				x.userID = caller.UserID
				serve(x)
			}}, nil
		})
	}
}

// exchange is one request being answered.
type exchange struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	userID    string
	routes    Routes
}

// id reads a path parameter that must be a positive row id. Anything else
// names no row, so it is the route's own "not found" and not a 422: the
// caller asked for a thing that does not exist.
func (x *exchange) id(name, absent string) (int64, bool) {
	value, err := strconv.ParseInt(x.ctx.Param(name), 10, 64)
	if err != nil || value < 1 {
		x.refuse(http.StatusNotFound, absent)
		return 0, false
	}
	return value, true
}

// paging reads limit and offset, with the web's defaults and bounds.
func (x *exchange) paging() (limit, offset int64, ok bool) {
	query := x.ctx.URL()
	read := func(name string, fallback, min, max int64) (int64, bool) {
		text := query.Query().Get(name)
		if text == "" {
			return fallback, true
		}
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil || value < min || value > max {
			x.routes.Errors.WriteValidation(x.response, x.requestID, []errorenvelope.Failure{{
				Location: []string{"query", name}, Message: "Input should be a whole number from " + strconv.FormatInt(min, 10) + " to " + strconv.FormatInt(max, 10)}})
			return 0, false
		}
		return value, true
	}
	if limit, ok = read("limit", 50, 1, 200); !ok {
		return 0, 0, false
	}
	// The upper bound is only what keeps the number an OFFSET Postgres
	// takes; a page that far in is simply empty.
	if offset, ok = read("offset", 0, 0, 1_000_000_000); !ok {
		return 0, 0, false
	}
	return limit, offset, true
}

// body decodes the request's JSON object into out, refusing a field out
// does not have: a misspelt key is said, not dropped.
func (x *exchange) body(out any) bool {
	decoder := json.NewDecoder(bytes.NewReader(requestbody.Buffered(x.ctx.Context())))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		x.invalid("The request body could not be read: " + err.Error())
		return false
	}
	return true
}

// invalid is a request that was understood and cannot be saved, in words.
func (x *exchange) invalid(problems ...string) {
	x.refuse(http.StatusUnprocessableEntity, strings.Join(problems, "; "))
}

func (x *exchange) refuse(status int, message string) {
	x.routes.Errors.Write(x.response, status, message, x.requestID)
}

func (x *exchange) send(status int, value any) {
	x.response.Header().Set("Content-Type", "application/json")
	x.response.Header().Set("Cache-Control", "private, no-store")
	x.response.WriteHeader(status)
	if value == nil {
		return
	}
	encoder := json.NewEncoder(x.response)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func (x *exchange) fail(reason string, cause error) {
	if !errors.Is(cause, context.Canceled) {
		attributes := []any{slog.String("request_id", x.requestID), slog.String("reason", reason)}
		if cause != nil {
			attributes = append(attributes, slog.String("error", cause.Error()))
		}
		x.routes.Logger.ErrorContext(x.ctx.Context(), "an automations route could not be served", attributes...)
	}
	x.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}

// detail answers one automation with its triggers.
func (x *exchange) detail(status int, automationID int64) {
	ctx, q := x.ctx.Context(), New(x.routes.Pool)
	row, err := q.GetAutomation(ctx, GetAutomationParams{ID: automationID, UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, automationAbsent)
		return
	}
	if err != nil {
		x.fail("could not read the automation", err)
		return
	}
	triggers, err := q.ListTriggers(ctx, ListTriggersParams{AutomationID: automationID, UserID: x.userID})
	if err != nil {
		x.fail("could not read the automation's triggers", err)
		return
	}
	out := detailView{
		summaryView: summaryOfAutomation(row.ID, row.Name, row.Description, row.Status, row.Version, row.CreatedAt, row.UpdatedAt),
		Definition:  object(row.Definition), Triggers: make([]triggerView, 0, len(triggers)),
	}
	for _, trigger := range triggers {
		out.Triggers = append(out.Triggers, triggerOf(trigger))
	}
	x.send(status, out)
}

func (r Routes) list(x *exchange) {
	limit, offset, ok := x.paging()
	if !ok {
		return
	}
	ctx, q := x.ctx.Context(), New(r.Pool)
	rows, err := q.ListAutomations(ctx, ListAutomationsParams{UserID: x.userID, PageSize: int32(limit), PageOffset: int32(offset)})
	if err != nil {
		x.fail("could not list the automations", err)
		return
	}
	total, err := q.CountAutomations(ctx, x.userID)
	if err != nil {
		x.fail("could not count the automations", err)
		return
	}
	out := page[summaryView]{Items: make([]summaryView, 0, len(rows)), Total: total}
	for _, row := range rows {
		out.Items = append(out.Items, summaryOfAutomation(row.ID, row.Name, row.Description, row.Status, row.Version, row.CreatedAt, row.UpdatedAt))
	}
	x.send(http.StatusOK, out)
}

// eligibility answers whether a run could be answered now. A run uses the
// account's chat model, so the one thing that can be wrong is that model:
// it is not offered, or the model service cannot be asked.
func (r Routes) eligibility(x *exchange) {
	type violation struct {
		Kind     string `json:"kind"`
		ConfigID *int64 `json:"config_id"`
		Reason   string `json:"reason"`
	}
	out := struct {
		Allowed    bool        `json:"allowed"`
		Violations []violation `json:"violations"`
	}{Allowed: true, Violations: []violation{}}
	chosen, err := r.Models.Look(x.ctx.Context(), x.userID)
	var unanswerable *assembly.ModelError
	switch {
	case errors.As(err, &unanswerable):
		out.Allowed, out.Violations = false, []violation{{Kind: "llm", Reason: unanswerable.Message}}
	case err != nil:
		x.fail("could not read the account's chat model", err)
		return
	case !chosen.Offered:
		out.Allowed = false
		out.Violations = []violation{{Kind: "llm", Reason: "The model that answers your chat, " + chosen.Name + ", is no longer offered. Choose another in the model menu."}}
	}
	x.send(http.StatusOK, out)
}

// newTrigger is a trigger as a request describes it.
type newTrigger struct {
	Type         string          `json:"type"`
	Params       json.RawMessage `json:"params"`
	StaticInputs json.RawMessage `json:"static_inputs"`
	Enabled      *bool           `json:"enabled"`
}

// checked is a trigger ready to be stored.
type checked struct {
	trigger      definition.Trigger
	staticInputs []byte
	enabled      bool
}

func (t newTrigger) check() (checked, error) {
	if len(t.Params) == 0 {
		t.Params = json.RawMessage(`{}`)
	}
	parsed, err := definition.ParseTrigger(t.Type, t.Params)
	if err != nil {
		return checked{}, err
	}
	static := t.StaticInputs
	if len(static) == 0 || string(static) == "null" {
		static = json.RawMessage(`{}`)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(static)), "{") {
		return checked{}, errors.New("static_inputs must be an object")
	}
	// A trigger is on unless the request says otherwise.
	return checked{trigger: parsed, staticInputs: static, enabled: t.Enabled == nil || *t.Enabled}, nil
}

// errModelsUnreachable is a save that names a model while the model proxy
// cannot be asked whether it is offered.
var errModelsUnreachable = errors.New("automations: the model proxy could not be asked")

// vouch checks what a definition names that only this tier can: that each
// notebook and connected account is the caller's own, that each model is
// one the proxy offers, and that this server sends mail if the result is
// to be emailed. The problems are in words for the person saving.
//
// Another account's notebook and one that never existed read the same
// here: neither is "one of yours".
func (r Routes) vouch(ctx context.Context, userID string, named definition.Named) ([]string, error) {
	var problems []string
	q := New(r.Pool)
	absent := func(what string, wanted, owned []int64) {
		for _, id := range wanted {
			if !slices.Contains(owned, id) {
				problems = append(problems, what+" "+strconv.FormatInt(id, 10)+" is not one of yours")
			}
		}
	}
	if len(named.NotebookIDs) > 0 {
		owned, err := q.OwnedNotebooks(ctx, OwnedNotebooksParams{UserID: userID, Ids: named.NotebookIDs})
		if err != nil {
			return nil, err
		}
		absent("notebook", named.NotebookIDs, owned)
	}
	if len(named.ConnectorIDs) > 0 {
		owned, err := q.OwnedConnections(ctx, OwnedConnectionsParams{UserID: userID, Ids: named.ConnectorIDs})
		if err != nil {
			return nil, err
		}
		absent("connected account", named.ConnectorIDs, owned)
	}
	if named.Email && !r.SendsMail {
		problems = append(problems, "execution.email_result is not available: this server is not set up to send mail")
	}
	withdrawn, err := r.Models.NotOffered(ctx, userID, named.Models)
	var unanswerable *assembly.ModelError
	switch {
	case errors.As(err, &unanswerable):
		return nil, errModelsUnreachable
	case err != nil:
		return nil, err
	}
	for _, name := range withdrawn {
		problems = append(problems, "the model "+strconv.Quote(name)+" is not offered; choose one from the list")
	}
	return problems, nil
}

// vouched answers the request when a definition names something this tier
// will not vouch for, and reports whether the save may go on.
func (x *exchange) vouched(parsed definition.Definition) bool {
	problems, err := x.routes.vouch(x.ctx.Context(), x.userID, parsed.Named())
	switch {
	case errors.Is(err, errModelsUnreachable):
		x.refuse(http.StatusServiceUnavailable, assembly.ModelsUnreachable)
	case err != nil:
		x.fail("could not check what the definition names", err)
	case len(problems) > 0:
		x.invalid(problems...)
	default:
		return true
	}
	return false
}

func (r Routes) create(x *exchange) {
	var request struct {
		Name        string          `json:"name"`
		Description *string         `json:"description"`
		Definition  json.RawMessage `json:"definition"`
		Triggers    []newTrigger    `json:"triggers"`
	}
	if !x.body(&request) {
		return
	}
	var problems []string
	if name := strings.TrimSpace(request.Name); name == "" || len([]rune(request.Name)) > definition.MaxNameChars {
		problems = append(problems, "name must be 1 to "+strconv.Itoa(definition.MaxNameChars)+" characters")
	}
	parsed, definitionProblems := definition.Parse(request.Definition)
	problems = append(problems, definitionProblems...)
	triggers := make([]checked, 0, len(request.Triggers))
	for i, trigger := range request.Triggers {
		ready, err := trigger.check()
		if err != nil {
			problems = append(problems, "triggers["+strconv.Itoa(i)+"]: "+err.Error())
			continue
		}
		triggers = append(triggers, ready)
	}
	if len(problems) > 0 {
		x.invalid(problems...)
		return
	}
	if !x.vouched(parsed) {
		return
	}
	stored, err := json.Marshal(parsed)
	if err != nil {
		x.fail("could not encode the definition", err)
		return
	}
	now := r.Now()
	// The automation and its first triggers are saved together or not at
	// all: an automation whose schedule failed to save would look made and
	// never run.
	id, err := database.Write(x.ctx.Context(), r.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (int64, error) {
		q := New(tx)
		id, err := q.CreateAutomation(ctx, CreateAutomationParams{UserID: x.userID, Name: request.Name, Description: request.Description, Definition: stored})
		if err != nil {
			return 0, err
		}
		for _, trigger := range triggers {
			if _, err := q.AddTrigger(ctx, AddTriggerParams{
				Type: AutomationTriggerType(trigger.trigger.Type), Params: trigger.trigger.Params, StaticInputs: trigger.staticInputs,
				Enabled: trigger.enabled, NextFireAt: trigger.trigger.NextFire(trigger.enabled, now), AutomationID: id, UserID: x.userID,
			}); err != nil {
				return 0, err
			}
		}
		return id, nil
	})
	if err != nil {
		x.fail("could not save the automation", err)
		return
	}
	x.detail(http.StatusCreated, id)
}

func (r Routes) read(x *exchange) {
	if id, ok := x.id("automation_id", automationAbsent); ok {
		x.detail(http.StatusOK, id)
	}
}

func (r Routes) update(x *exchange) {
	id, ok := x.id("automation_id", automationAbsent)
	if !ok {
		return
	}
	// Each field is a pointer or raw JSON so that "not mentioned" and
	// "null" can be told from a value: a field that is absent or null is
	// left alone, except description, where null clears it.
	var request struct {
		Name        *string         `json:"name"`
		Description json.RawMessage `json:"description"`
		Status      *string         `json:"status"`
		Definition  json.RawMessage `json:"definition"`
	}
	if !x.body(&request) {
		return
	}
	arg := UpdateAutomationParams{ID: id, UserID: x.userID, Status: AutomationStatusActive}
	var problems []string
	if request.Name != nil {
		if name := strings.TrimSpace(*request.Name); name == "" || len([]rune(*request.Name)) > definition.MaxNameChars {
			problems = append(problems, "name must be 1 to "+strconv.Itoa(definition.MaxNameChars)+" characters")
		}
		arg.SetName, arg.Name = true, *request.Name
	}
	if len(request.Description) > 0 {
		arg.SetDescription = true
		if string(request.Description) != "null" {
			var text string
			if err := json.Unmarshal(request.Description, &text); err != nil {
				problems = append(problems, "description must be text or null")
			}
			arg.Description = &text
		}
	}
	if request.Status != nil {
		switch AutomationStatus(*request.Status) {
		case AutomationStatusActive, AutomationStatusPaused, AutomationStatusArchived:
			arg.SetStatus, arg.Status = true, AutomationStatus(*request.Status)
		default:
			problems = append(problems, "status must be active, paused or archived")
		}
	}
	var parsed definition.Definition
	if len(request.Definition) > 0 && string(request.Definition) != "null" {
		var definitionProblems []string
		parsed, definitionProblems = definition.Parse(request.Definition)
		problems = append(problems, definitionProblems...)
		arg.SetDefinition = true
	}
	if len(problems) > 0 {
		x.invalid(problems...)
		return
	}
	// A definition replaces the one saved, whole: one that names no model
	// runs on the account's own from now on, whatever the old one named.
	if arg.SetDefinition && !x.vouched(parsed) {
		return
	}
	found, err := database.Write(x.ctx.Context(), r.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (bool, error) {
		q := New(tx)
		if arg.SetDefinition {
			stored, err := json.Marshal(parsed)
			if err != nil {
				return false, err
			}
			arg.Definition = stored
		}
		if _, err := q.UpdateAutomation(ctx, arg); errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		} else if err != nil {
			return false, err
		}
		if arg.SetStatus && arg.Status != AutomationStatusActive {
			// Pausing stops what is in flight, in the transaction that
			// pauses: no run can start between the two.
			reason, err := json.Marshal(map[string]string{"type": "cancelled", "message": "The automation was " + string(arg.Status) + "."})
			if err != nil {
				return false, err
			}
			if _, err := q.CancelUnfinishedRuns(ctx, CancelUnfinishedRunsParams{Error: reason, AutomationID: id, UserID: x.userID}); err != nil {
				return false, err
			}
		}
		return true, nil
	})
	if err != nil {
		x.fail("could not update the automation", err)
		return
	}
	if !found {
		x.refuse(http.StatusNotFound, automationAbsent)
		return
	}
	x.detail(http.StatusOK, id)
}

func (r Routes) remove(x *exchange) {
	id, ok := x.id("automation_id", automationAbsent)
	if !ok {
		return
	}
	// The hidden threads its runs made artifacts under go first, in the
	// same transaction: once the runs are gone nothing names them.
	removed, err := database.Write(x.ctx.Context(), r.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (int64, error) {
		q := New(tx)
		if _, err := q.DeleteRunThreads(ctx, DeleteRunThreadsParams{ID: id, UserID: x.userID}); err != nil {
			return 0, err
		}
		return q.DeleteAutomation(ctx, DeleteAutomationParams{ID: id, UserID: x.userID})
	})
	switch {
	case err != nil:
		x.fail("could not delete the automation", err)
	case removed == 0:
		x.refuse(http.StatusNotFound, automationAbsent)
	default:
		x.send(http.StatusNoContent, nil)
	}
}

// runNow starts a run by hand. The run is a row before it is a message, so
// a broker that is down does not lose it: the worker's tick publishes any
// run that has waited without one.
func (r Routes) runNow(x *exchange) {
	id, ok := x.id("automation_id", automationAbsent)
	if !ok {
		return
	}
	ctx := x.ctx.Context()
	inputs, err := json.Marshal(map[string]string{"started_by": "hand", "fired_at": r.Now().UTC().Format(time.RFC3339)})
	if err != nil {
		x.fail("could not encode the run's inputs", err)
		return
	}
	runID, outcome, err := launch.Run(ctx, r.Pool, launch.Request{AutomationID: id, UserID: x.userID, Inputs: inputs})
	switch {
	case err != nil:
		x.fail("could not start the run", err)
		return
	case outcome == launch.NotFound:
		x.refuse(http.StatusNotFound, automationAbsent)
		return
	case outcome == launch.NotActive:
		x.refuse(http.StatusConflict, notActive)
		return
	case outcome == launch.AlreadyRunning:
		x.refuse(http.StatusConflict, alreadyRunning)
		return
	}
	if _, err := r.Queue.Enqueue(ctx, runqueue.Execute(runID, 0)); err != nil {
		r.Logger.WarnContext(ctx, "a run's message was not published; the worker's tick will publish it",
			slog.Int64("run_id", runID), slog.String("request_id", x.requestID), slog.String("error", err.Error()))
	}
	x.runSummary(http.StatusAccepted, id, runID)
}

func (r Routes) addTrigger(x *exchange) {
	id, ok := x.id("automation_id", automationAbsent)
	if !ok {
		return
	}
	var request newTrigger
	if !x.body(&request) {
		return
	}
	ready, err := request.check()
	if err != nil {
		x.invalid(err.Error())
		return
	}
	ctx, q := x.ctx.Context(), New(r.Pool)
	triggerID, err := q.AddTrigger(ctx, AddTriggerParams{
		Type: AutomationTriggerType(ready.trigger.Type), Params: ready.trigger.Params, StaticInputs: ready.staticInputs,
		Enabled: ready.enabled, NextFireAt: ready.trigger.NextFire(ready.enabled, r.Now()), AutomationID: id, UserID: x.userID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, automationAbsent)
		return
	}
	if err != nil {
		x.fail("could not add the trigger", err)
		return
	}
	x.trigger(http.StatusCreated, id, triggerID)
}

// trigger answers one trigger.
func (x *exchange) trigger(status int, automationID, triggerID int64) {
	row, err := New(x.routes.Pool).GetTrigger(x.ctx.Context(), GetTriggerParams{ID: triggerID, AutomationID: automationID, UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, triggerAbsent)
		return
	}
	if err != nil {
		x.fail("could not read the trigger", err)
		return
	}
	x.send(status, triggerOf(ListTriggersRow(row)))
}

func (r Routes) updateTrigger(x *exchange) {
	id, ok := x.id("automation_id", automationAbsent)
	if !ok {
		return
	}
	triggerID, ok := x.id("trigger_id", triggerAbsent)
	if !ok {
		return
	}
	// A trigger's type cannot change; the three things that can are each
	// left alone when absent or null.
	var request struct {
		Enabled      *bool           `json:"enabled"`
		Params       json.RawMessage `json:"params"`
		StaticInputs json.RawMessage `json:"static_inputs"`
	}
	if !x.body(&request) {
		return
	}
	ctx, q := x.ctx.Context(), New(r.Pool)
	current, err := q.GetTrigger(ctx, GetTriggerParams{ID: triggerID, AutomationID: id, UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, triggerAbsent)
		return
	}
	if err != nil {
		x.fail("could not read the trigger", err)
		return
	}
	merged := newTrigger{Type: current.Type, Params: current.Params, StaticInputs: current.StaticInputs, Enabled: &current.Enabled}
	if len(request.Params) > 0 && string(request.Params) != "null" {
		merged.Params = request.Params
	}
	if len(request.StaticInputs) > 0 && string(request.StaticInputs) != "null" {
		merged.StaticInputs = request.StaticInputs
	}
	if request.Enabled != nil {
		merged.Enabled = request.Enabled
	}
	ready, err := merged.check()
	if err != nil {
		x.invalid(err.Error())
		return
	}
	// A schedule's next fire is worked out again from now on every change:
	// new timing takes effect at once, and a trigger switched back on does
	// not fire for the moments it was off.
	changed, err := q.UpdateTrigger(ctx, UpdateTriggerParams{
		Params: ready.trigger.Params, StaticInputs: ready.staticInputs, Enabled: ready.enabled,
		NextFireAt: ready.trigger.NextFire(ready.enabled, r.Now()), ID: triggerID, AutomationID: id, UserID: x.userID,
	})
	if err != nil {
		x.fail("could not update the trigger", err)
		return
	}
	if changed == 0 {
		x.refuse(http.StatusNotFound, triggerAbsent)
		return
	}
	x.trigger(http.StatusOK, id, triggerID)
}

func (r Routes) removeTrigger(x *exchange) {
	id, ok := x.id("automation_id", automationAbsent)
	if !ok {
		return
	}
	triggerID, ok := x.id("trigger_id", triggerAbsent)
	if !ok {
		return
	}
	removed, err := New(r.Pool).DeleteTrigger(x.ctx.Context(), DeleteTriggerParams{ID: triggerID, AutomationID: id, UserID: x.userID})
	switch {
	case err != nil:
		x.fail("could not remove the trigger", err)
	case removed == 0:
		x.refuse(http.StatusNotFound, triggerAbsent)
	default:
		x.send(http.StatusNoContent, nil)
	}
}

func (r Routes) listRuns(x *exchange) {
	id, ok := x.id("automation_id", automationAbsent)
	if !ok {
		return
	}
	limit, offset, ok := x.paging()
	if !ok {
		return
	}
	ctx, q := x.ctx.Context(), New(r.Pool)
	// The automation first, so that another account's is not found rather
	// than answered as an automation with no runs.
	if _, err := q.GetAutomation(ctx, GetAutomationParams{ID: id, UserID: x.userID}); errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, automationAbsent)
		return
	} else if err != nil {
		x.fail("could not read the automation", err)
		return
	}
	rows, err := q.ListRuns(ctx, ListRunsParams{AutomationID: id, UserID: x.userID, PageSize: int32(limit), PageOffset: int32(offset)})
	if err != nil {
		x.fail("could not list the runs", err)
		return
	}
	total, err := q.CountRuns(ctx, CountRunsParams{AutomationID: id, UserID: x.userID})
	if err != nil {
		x.fail("could not count the runs", err)
		return
	}
	out := page[runSummaryView]{Items: make([]runSummaryView, 0, len(rows)), Total: total}
	for _, row := range rows {
		out.Items = append(out.Items, summaryOfRun(row.ID, row.AutomationID, row.TriggerID, row.Status, row.StartedAt, row.FinishedAt, row.CreatedAt))
	}
	x.send(http.StatusOK, out)
}

// run reads one run of the caller's automation.
func (x *exchange) run(automationID, runID int64) (GetRunRow, bool) {
	row, err := New(x.routes.Pool).GetRun(x.ctx.Context(), GetRunParams{ID: runID, AutomationID: automationID, UserID: x.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, runAbsent)
		return GetRunRow{}, false
	}
	if err != nil {
		x.fail("could not read the run", err)
		return GetRunRow{}, false
	}
	return row, true
}

func summaryOf(row GetRunRow) runSummaryView {
	return summaryOfRun(row.ID, row.AutomationID, row.TriggerID, row.Status, row.StartedAt, row.FinishedAt, row.CreatedAt)
}

func (x *exchange) runSummary(status int, automationID, runID int64) {
	if row, ok := x.run(automationID, runID); ok {
		x.send(status, summaryOf(row))
	}
}

func (r Routes) readRun(x *exchange) {
	id, ok := x.id("automation_id", automationAbsent)
	if !ok {
		return
	}
	runID, ok := x.id("run_id", runAbsent)
	if !ok {
		return
	}
	row, ok := x.run(id, runID)
	if !ok {
		return
	}
	x.send(http.StatusOK, runView{
		runSummaryView: summaryOf(row), DefinitionSnapshot: object(row.DefinitionSnapshot), Inputs: object(row.Inputs),
		StepResults: list(row.StepResults), Output: nullable(row.Output), Artifacts: list(row.Artifacts), Error: nullable(row.Error),
	})
}

func (r Routes) cancelRun(x *exchange) {
	id, ok := x.id("automation_id", automationAbsent)
	if !ok {
		return
	}
	runID, ok := x.id("run_id", runAbsent)
	if !ok {
		return
	}
	reason, err := json.Marshal(map[string]string{"type": "cancelled", "message": "The run was cancelled."})
	if err != nil {
		x.fail("could not encode the cancellation", err)
		return
	}
	changed, err := New(r.Pool).CancelRun(x.ctx.Context(), CancelRunParams{Error: reason, ID: runID, AutomationID: id, UserID: x.userID})
	if err != nil {
		x.fail("could not cancel the run", err)
		return
	}
	row, ok := x.run(id, runID)
	if !ok {
		return
	}
	if changed == 0 {
		// The run is the caller's and had already ended some other way.
		x.refuse(http.StatusConflict, runFinished)
		return
	}
	x.send(http.StatusOK, summaryOf(row))
}
