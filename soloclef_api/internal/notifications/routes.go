package notifications

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Routes is the eight routes.
type Routes struct {
	Pool   *pgxpool.Pool
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

type (
	noPath  struct{}
	onePath struct {
		NotificationID string `path:"notification_id"`
	}
)

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	mount[noPath](api, r, http.MethodGet, ListPath, "list-notifications", "One page of the inbox", r.list)
	mount[noPath](api, r, http.MethodDelete, ListPath, "clear-notifications", "Delete every notification of an inbox view", r.clear)
	mount[noPath](api, r, http.MethodPatch, ReadAllPath, "read-all-notifications", "Mark every notification read", r.readAll)
	mount[onePath](api, r, http.MethodPatch, ReadPath, "read-notification", "Mark one notification read", r.read)
	mount[onePath](api, r, http.MethodDelete, OnePath, "delete-notification", "Delete one notification", r.remove)
	mount[noPath](api, r, http.MethodGet, UnreadCountPath, "unread-notification-count", "How many notifications are unread", r.unreadCount)
	mount[noPath](api, r, http.MethodGet, UnreadBatchPath, "unread-notification-counts", "How many notifications are unread in each tab", r.unreadBatch)
	mount[noPath](api, r, http.MethodGet, SourceTypesPath, "notification-source-types", "The source types the Status tab can filter by", r.sourceTypes)
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

type exchange struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	userID    string
	routes    Routes
}

func mount[P any](api huma.API, r Routes, method, path, id, summary string, serve func(*exchange)) {
	huma.Register(api, huma.Operation{
		OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"notifications"},
	}, func(_ context.Context, _ *P) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			x := &exchange{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), routes: r}
			caller, ok := principal.FromContext(ctx.Context())
			if !ok {
				x.fail("the route ran without a caller", nil)
				return
			}
			if !caller.IsSession() {
				x.refuse(http.StatusForbidden, sessionRequired)
				return
			}
			x.userID = caller.UserID
			serve(x)
		}}, nil
	})
}

// id reads the path's whole number; a value that is not one is a 422. An id
// too wide for a bigint names nothing, so it reads as 0, which no row has.
func (x *exchange) id() (int64, bool) {
	text := x.ctx.Param("notification_id")
	value, err := strconv.ParseInt(text, 10, 64)
	if text == "" || err != nil && strings.Trim(text, "0123456789") != "" {
		x.routes.Errors.WriteValidation(x.response, x.requestID, []errorenvelope.Failure{{
			Location: []string{"path", "notification_id"}, Message: "Input should be a valid integer"}})
		return 0, false
	}
	return value, true
}

// view reads the request's filters, or answers the 422 that names each one
// that was not understood.
func (x *exchange) view(paging bool) (view, bool) {
	address := x.ctx.URL()
	asked, failures := readView(address.Query(), paging)
	if len(failures) > 0 {
		x.routes.Errors.WriteValidation(x.response, x.requestID, failures)
		return view{}, false
	}
	return asked, true
}

func (x *exchange) refuse(status int, message string) {
	x.routes.Errors.Write(x.response, status, message, x.requestID)
}

func (x *exchange) send(value any) {
	x.response.Header().Set("Content-Type", "application/json")
	x.response.Header().Set("Cache-Control", "private, no-store")
	x.response.WriteHeader(http.StatusOK)
	encoder := json.NewEncoder(x.response)
	// A title is JSON, not HTML: "<" stays "<".
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value)
}

func (x *exchange) fail(reason string, cause error) {
	if !errors.Is(cause, context.Canceled) {
		attributes := []any{slog.String("request_id", x.requestID), slog.String("reason", reason)}
		if cause != nil {
			attributes = append(attributes, slog.String("error", cause.Error()))
		}
		x.routes.Logger.ErrorContext(x.ctx.Context(), "an inbox route could not be served", attributes...)
	}
	x.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}

// item is one notification as the inbox reads it.
type item struct {
	ID       int64           `json:"id"`
	UserID   string          `json:"user_id"`
	Type     string          `json:"type"`
	Title    string          `json:"title"`
	Message  string          `json:"message"`
	Read     bool            `json:"read"`
	Metadata json.RawMessage `json:"metadata"`
	Created  time.Time       `json:"created_at"`
	Updated  *time.Time      `json:"updated_at"`
}

// emptyObject is the metadata of a row that has none: the client reads the
// field as an object, always.
var emptyObject = json.RawMessage(`{}`)

func itemOf(row ListViewRow) item {
	out := item{ID: row.ID, UserID: row.UserID, Type: row.Type, Title: row.Title, Message: row.Message,
		Read: row.Read, Metadata: emptyObject, Created: row.CreatedAt.UTC(), Updated: row.UpdatedAt}
	// Only an object is passed on. A row whose metadata is some other JSON
	// (a null, a list) is still listed, with none.
	if trimmed := strings.TrimSpace(string(row.Metadata)); strings.HasPrefix(trimmed, "{") {
		out.Metadata = json.RawMessage(trimmed)
	}
	if out.Updated != nil {
		utc := out.Updated.UTC()
		out.Updated = &utc
	}
	return out
}

func (r Routes) list(x *exchange) {
	asked, ok := x.view(true)
	if !ok {
		return
	}
	ctx, q, now := x.ctx.Context(), New(r.Pool), r.now()
	// One row more than the page, to know whether another page follows.
	rows, err := q.ListView(ctx, ListViewParams{
		UserID: x.userID, Now: now, Types: asked.typesArgument(), SourceKey: asked.sourceKey, SourceValue: asked.sourceValue,
		UnreadOnly: asked.unreadOnly, ErrorsOnly: asked.errorsOnly, SearchPattern: asked.searchPattern, Before: asked.before,
		PageOffset: int32(asked.offset), PageSize: int32(asked.limit + 1),
	})
	if err != nil {
		x.fail("could not read the inbox", err)
		return
	}
	var total int64
	switch {
	case len(rows) > 0:
		total = rows[0].Total
	case asked.offset > 0:
		// A page past the end carries no row to read the total from.
		total, err = q.CountView(ctx, CountViewParams{
			UserID: x.userID, Now: now, Types: asked.typesArgument(), SourceKey: asked.sourceKey, SourceValue: asked.sourceValue,
			UnreadOnly: asked.unreadOnly, ErrorsOnly: asked.errorsOnly, SearchPattern: asked.searchPattern, Before: asked.before,
		})
		if err != nil {
			x.fail("could not count the inbox", err)
			return
		}
	}
	more := len(rows) > asked.limit
	if more {
		rows = rows[:asked.limit]
	}
	answer := struct {
		Items      []item `json:"items"`
		Total      int64  `json:"total"`
		HasMore    bool   `json:"has_more"`
		NextOffset *int   `json:"next_offset"`
	}{Items: make([]item, 0, len(rows)), Total: total, HasMore: more}
	for _, row := range rows {
		answer.Items = append(answer.Items, itemOf(row))
	}
	if more {
		next := asked.offset + asked.limit
		answer.NextOffset = &next
	}
	x.send(answer)
}

func (r Routes) clear(x *exchange) {
	asked, ok := x.view(false)
	if !ok {
		return
	}
	deleted, err := New(r.Pool).ClearView(x.ctx.Context(), ClearViewParams{
		UserID: x.userID, Now: r.now(), Types: asked.typesArgument(), SourceKey: asked.sourceKey, SourceValue: asked.sourceValue,
		UnreadOnly: asked.unreadOnly, ErrorsOnly: asked.errorsOnly, SearchPattern: asked.searchPattern,
	})
	if err != nil {
		x.fail("could not clear the inbox", err)
		return
	}
	x.send(struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Deleted int64  `json:"deleted_count"`
	}{true, fmt.Sprintf("Deleted %d notification(s)", deleted), deleted})
}

type outcome struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

func (r Routes) remove(x *exchange) {
	id, ok := x.id()
	if !ok {
		return
	}
	ctx, q := x.ctx.Context(), New(r.Pool)
	// The clear's statement, narrowed to one row: the same rule about a
	// running job and the same care for an announcement's read count.
	deleted, err := q.ClearView(ctx, ClearViewParams{UserID: x.userID, Now: r.now(), Types: []string{}, OnlyID: id})
	if err != nil {
		x.fail("could not delete the notification", err)
		return
	}
	if deleted == 0 {
		mine, err := q.IsMine(ctx, IsMineParams{ID: id, UserID: x.userID})
		switch {
		case err != nil:
			x.fail("could not look for the notification", err)
		case mine:
			x.refuse(http.StatusConflict, stillRunning)
		default:
			x.refuse(http.StatusNotFound, absent)
		}
		return
	}
	x.send(outcome{true, "Notification deleted"})
}

func (r Routes) read(x *exchange) {
	id, ok := x.id()
	if !ok {
		return
	}
	wasRead, err := New(r.Pool).MarkRead(x.ctx.Context(), MarkReadParams{Now: r.now(), ID: id, UserID: x.userID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		x.refuse(http.StatusNotFound, absent)
	case err != nil:
		x.fail("could not mark the notification read", err)
	case wasRead:
		x.send(outcome{true, "Notification already marked as read"})
	default:
		x.send(outcome{true, "Notification marked as read"})
	}
}

func (r Routes) readAll(x *exchange) {
	updated, err := New(r.Pool).MarkAllRead(x.ctx.Context(), MarkAllReadParams{Now: r.now(), UserID: x.userID})
	if err != nil {
		x.fail("could not mark the inbox read", err)
		return
	}
	x.send(struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Updated int64  `json:"updated_count"`
	}{true, fmt.Sprintf("Marked %d notification(s) as read", updated), updated})
}

type unread struct {
	Total  int64 `json:"total_unread"`
	Recent int64 `json:"recent_unread"`
}

// counts reads every unread count in one pass, narrowed to one type when
// onlyType is not "".
func (x *exchange) counts(onlyType string) (UnreadCountsRow, bool) {
	now := x.routes.now()
	row, err := New(x.routes.Pool).UnreadCounts(x.ctx.Context(), UnreadCountsParams{
		StatusTypes: statusTypes, RecentAfter: now.Add(-recentWindow), UserID: x.userID, OnlyType: onlyType, Now: now,
	})
	if err != nil {
		x.fail("could not count the unread notifications", err)
		return UnreadCountsRow{}, false
	}
	return row, true
}

func (r Routes) unreadCount(x *exchange) {
	address := x.ctx.URL()
	query := address.Query()
	// The two filters of the view that a count takes; the rest are a page's.
	asked, failures := readView(map[string][]string{"type": query["type"], "category": query["category"]}, false)
	if len(failures) > 0 {
		r.Errors.WriteValidation(x.response, x.requestID, failures)
		return
	}
	onlyType := query.Get("type")
	row, ok := x.counts(onlyType)
	if !ok {
		return
	}
	answer := unread{row.AllTotal, row.AllRecent}
	switch category := query.Get("category"); {
	case len(asked.types) == 1 && asked.types[0] == "\x00none":
		// A type asked for inside a tab that does not hold it.
		answer = unread{}
	case category == "status":
		answer = unread{row.StatusTotal, row.StatusRecent}
	case category == "announcements":
		answer = unread{row.AnnouncementsTotal, row.AnnouncementsRecent}
	}
	x.send(answer)
}

func (r Routes) unreadBatch(x *exchange) {
	row, ok := x.counts("")
	if !ok {
		return
	}
	x.send(struct {
		Status        unread `json:"status"`
		Announcements unread `json:"announcements"`
	}{unread{row.StatusTotal, row.StatusRecent}, unread{row.AnnouncementsTotal, row.AnnouncementsRecent}})
}

func (r Routes) sourceTypes(x *exchange) {
	rows, err := New(r.Pool).SourceTypes(x.ctx.Context(), x.userID)
	if err != nil {
		x.fail("could not read the inbox's source types", err)
		return
	}
	type source struct {
		Key      string `json:"key"`
		Type     string `json:"type"`
		Category string `json:"category"`
		Count    int64  `json:"count"`
	}
	answer := struct {
		Sources []source `json:"sources"`
	}{Sources: make([]source, 0, len(rows))}
	for _, row := range rows {
		answer.Sources = append(answer.Sources, source{row.Category + ":" + row.SourceType, row.SourceType, row.Category, row.Rows})
	}
	x.send(answer)
}
