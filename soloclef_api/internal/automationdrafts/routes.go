package automationdrafts

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_automations/definition"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Routes serves the two routes. Every field is required.
type Routes struct {
	Pool   *pgxpool.Pool
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (r Routes) Check() error {
	if r.Pool == nil || r.Logger == nil {
		return errors.New("automationdrafts: Pool and Logger are required")
	}
	return nil
}

// Register adds the two operations.
func (r Routes) Register(api huma.API) {
	type draft struct {
		DraftID string `path:"draft_id" doc:"The draft's id"`
	}
	for _, route := range []struct {
		method, path, id, summary string
		serve                     func(call)
	}{
		{http.MethodGet, DraftPath, "get-automation-draft", "An automation the chat drafted, as its card shows it", r.read},
		{http.MethodPost, DiscardPath, "discard-automation-draft", "Set aside an automation draft that is still waiting", r.discard},
	} {
		serve := route.serve
		huma.Register(api, huma.Operation{
			OperationID: route.id, Method: route.method, Path: route.path, Summary: route.summary, Tags: []string{"automations"},
		}, func(_ context.Context, _ *draft) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: func(ctx huma.Context) {
				c := call{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), routes: r}
				caller, ok := principal.FromContext(ctx.Context())
				if !ok {
					c.fail("the route ran without a caller", nil)
					return
				}
				c.userID = caller.UserID
				// A draft id is handed out in one form; anything else names
				// no draft, and is never given to Postgres to cast.
				c.draftID = ctx.Param("draft_id")
				if _, err := uuid.Parse(c.draftID); err != nil || len(c.draftID) != 36 {
					c.refuse(http.StatusNotFound, draftAbsent)
					return
				}
				serve(c)
			}}, nil
		})
	}
}

// call is one request being answered.
type call struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	userID    string
	draftID   string
	routes    Routes
}

func (c call) refuse(status int, message string) {
	c.routes.Errors.Write(c.response, status, message, c.requestID)
}

func (c call) fail(reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", c.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	c.routes.Logger.LogAttrs(c.ctx.Context(), slog.LevelError, "an automation draft route failed", attributes...)
	c.routes.Errors.Write(c.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, c.requestID)
}

// folderView names one folder a draft's trigger refers to.
type folderView struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

// savedView is the automation a created draft became.
type savedView struct {
	Name     string         `json:"name"`
	Triggers []savedTrigger `json:"triggers"`
}

type savedTrigger struct {
	Type   string          `json:"type"`
	Params json.RawMessage `json:"params"`
}

// view is the draft as the card reads it.
type view struct {
	ID string `json:"id"`
	// Status is pending, created, discarded or superseded.
	Status string `json:"status"`
	// Request is the create request as drafted: exactly the body POST
	// /api/v1/automations takes, which the card edits and sends.
	Request      json.RawMessage `json:"request"`
	AutomationID *int64          `json:"automation_id"`
	ReplacedBy   *string         `json:"replaced_by"`
	// Automation is what Create saved, as it now is: the card of a created
	// draft shows this, not the request as drafted, because the person may
	// have changed any field on the card first. Nil until created, and
	// after the automation was deleted.
	Automation *savedView `json:"automation"`
	// Folders names the folders the draft's event trigger waits on, so
	// the card can say "when a document arrives in Papers" without a
	// listing of every folder.
	Folders   []folderView `json:"folders"`
	CreatedAt time.Time    `json:"created_at"`
}

func (r Routes) read(c call) {
	r.answer(c, http.StatusOK)
}

func (r Routes) discard(c call) {
	ctx, q := c.ctx.Context(), New(r.Pool)
	changed, err := q.DiscardDraft(ctx, DiscardDraftParams{ID: c.draftID, UserID: c.userID})
	if err != nil {
		c.fail("could not discard the draft", err)
		return
	}
	if changed == 0 {
		// Nothing changed: the draft is not the caller's, or is no longer
		// waiting. Reading it tells the two apart without a second rule.
		if _, err := q.ReadDraft(ctx, ReadDraftParams{ID: c.draftID, UserID: c.userID}); errors.Is(err, pgx.ErrNoRows) {
			c.refuse(http.StatusNotFound, draftAbsent)
		} else if err != nil {
			c.fail("could not read the draft", err)
		} else {
			c.refuse(http.StatusConflict, draftNotWaiting)
		}
		return
	}
	r.answer(c, http.StatusOK)
}

// answer sends the draft as it now is.
func (r Routes) answer(c call, status int) {
	ctx, q := c.ctx.Context(), New(r.Pool)
	row, err := q.ReadDraft(ctx, ReadDraftParams{ID: c.draftID, UserID: c.userID})
	if errors.Is(err, pgx.ErrNoRows) {
		c.refuse(http.StatusNotFound, draftAbsent)
		return
	}
	if err != nil {
		c.fail("could not read the draft", err)
		return
	}
	out := view{ID: row.ID, Status: row.Status, Request: row.Request, AutomationID: row.AutomationID,
		ReplacedBy: row.ReplacedBy, Folders: []folderView{}, CreatedAt: row.CreatedAt}
	ids := eventFolders(row.Request)
	if row.AutomationID != nil {
		saved, err := q.CreatedAutomation(ctx, CreatedAutomationParams{AutomationID: *row.AutomationID, UserID: c.userID})
		if err != nil {
			c.fail("could not read the created automation", err)
			return
		}
		if len(saved) > 0 {
			out.Automation = &savedView{Name: saved[0].Name, Triggers: []savedTrigger{}}
			for _, trigger := range saved {
				if trigger.Type == "" {
					continue
				}
				out.Automation.Triggers = append(out.Automation.Triggers, savedTrigger{Type: trigger.Type, Params: trigger.Params})
				if id := folderOf(trigger.Type, trigger.Params); id > 0 {
					ids = append(ids, id)
				}
			}
		}
	}
	if len(ids) > 0 {
		folders, err := q.FolderTitles(ctx, FolderTitlesParams{UserID: c.userID, Ids: ids})
		if err != nil {
			c.fail("could not name the draft's folders", err)
			return
		}
		for _, folder := range folders {
			out.Folders = append(out.Folders, folderView{ID: folder.ID, Title: folder.Name})
		}
	}
	c.response.Header().Set("Content-Type", "application/json")
	// The draft changes when Create or Discard is pressed elsewhere; a
	// cached copy would offer a Create that is no longer possible.
	c.response.Header().Set("Cache-Control", "private, no-store")
	c.response.WriteHeader(status)
	encoder := json.NewEncoder(c.response)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(out)
}

// eventFolders are the folders the request's event triggers wait on. A
// request that does not decode names no folder: it was checked when it was
// drafted, so this is not where it is judged.
func eventFolders(request []byte) []int64 {
	var drafted struct {
		Triggers []struct {
			Type   string          `json:"type"`
			Params json.RawMessage `json:"params"`
		} `json:"triggers"`
	}
	_ = json.Unmarshal(request, &drafted)
	var ids []int64
	for _, trigger := range drafted.Triggers {
		if id := folderOf(trigger.Type, trigger.Params); id > 0 {
			ids = append(ids, id)
		}
	}
	return ids
}

// folderOf is the folder an event trigger waits on: the folder_id of a
// document.entered_folder filter, the one filter the chat drafts; 0 for any
// other trigger, or a filter written some other way (the card then shows
// the trigger as it is).
func folderOf(kind string, params []byte) int64 {
	if kind != definition.TriggerEvent {
		return 0
	}
	var event struct {
		EventType string `json:"event_type"`
		Filter    struct {
			FolderID int64 `json:"folder_id"`
		} `json:"filter"`
	}
	if json.Unmarshal(params, &event) != nil || event.EventType != definition.DocumentEnteredFolder {
		return 0
	}
	return event.Filter.FolderID
}
