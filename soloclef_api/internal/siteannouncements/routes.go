package siteannouncements

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Routes is the three routes.
type Routes struct {
	Pool   *pgxpool.Pool
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	r.mount(api, http.MethodGet, ListPath, "list-announcements", "The announcements an administrator sent", r.list)
	r.mount(api, http.MethodPost, ListPath, "send-announcement", "Send an announcement to every account", r.send)
	huma.Register(api, huma.Operation{
		OperationID: "withdraw-announcement", Method: http.MethodPost, Path: WithdrawPath,
		Summary: "Take an announcement out of every inbox", Tags: []string{"site-announcements"},
	}, func(_ context.Context, _ *struct {
		AnnouncementID string `path:"announcement_id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) { r.serve(ctx, r.withdraw) }}, nil
	})
}

func (r Routes) mount(api huma.API, method, path, id, summary string, serve func(*exchange)) {
	huma.Register(api, huma.Operation{
		OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"site-announcements"},
	}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) { r.serve(ctx, serve) }}, nil
	})
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

// exchange is one request, already known to be an administrator's.
type exchange struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	userID    string
	routes    Routes
}

// serve lets an administrator's session through to the route and refuses
// everyone else, before anything is read or written.
func (r Routes) serve(ctx huma.Context, route func(*exchange)) {
	x := &exchange{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), routes: r}
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		x.fail("the route ran without a caller", nil)
		return
	}
	// A key or an agent's token is refused before the flag is even read
	// (see the package comment).
	if caller.Method != principal.MethodSession {
		x.refuse(http.StatusForbidden, administratorsOnly)
		return
	}
	administrator, err := New(r.Pool).AccountIsAdministrator(ctx.Context(), caller.UserID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		x.fail("could not read whether the caller is an administrator", err)
		return
	}
	if !administrator {
		x.refuse(http.StatusForbidden, administratorsOnly)
		return
	}
	x.userID = caller.UserID
	route(x)
}

func (x *exchange) refuse(status int, message string) {
	x.routes.Errors.Write(x.response, status, message, x.requestID)
}

func (x *exchange) send(status int, value any) {
	x.response.Header().Set("Content-Type", "application/json")
	x.response.Header().Set("Cache-Control", "private, no-store")
	x.response.WriteHeader(status)
	_ = json.NewEncoder(x.response).Encode(value)
}

func (x *exchange) fail(reason string, cause error) {
	if !errors.Is(cause, context.Canceled) {
		attributes := []any{slog.String("request_id", x.requestID), slog.String("reason", reason)}
		if cause != nil {
			attributes = append(attributes, slog.String("error", cause.Error()))
		}
		x.routes.Logger.ErrorContext(x.ctx.Context(), "an announcements route could not be served", attributes...)
	}
	x.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}

// sent is one announcement as the Sent list shows it.
type sent struct {
	ID             int64      `json:"id"`
	Title          string     `json:"title"`
	Body           string     `json:"body"`
	Link           *string    `json:"link"`
	CreatedAt      time.Time  `json:"created_at"`
	ExpiresAt      *time.Time `json:"expires_at"`
	WithdrawnAt    *time.Time `json:"withdrawn_at"`
	RecipientCount int64      `json:"recipient_count"`
	ReadCount      int64      `json:"read_count"`
	Status         string     `json:"status"`
}

// statusAt is whether inboxes still show it: withdrawn wins over expired,
// because a withdrawal is the administrator's own act and the list should
// say so.
func statusAt(withdrawnAt, expiresAt *time.Time, now time.Time) string {
	switch {
	case withdrawnAt != nil:
		return "withdrawn"
	case expiresAt != nil && !expiresAt.After(now):
		return "expired"
	}
	return "live"
}

func (r Routes) list(x *exchange) {
	rows, err := New(r.Pool).RecentAnnouncements(x.ctx.Context(), sentListSize)
	if err != nil {
		x.fail("could not read the announcements", err)
		return
	}
	now := r.now()
	listed := make([]sent, 0, len(rows))
	for _, row := range rows {
		listed = append(listed, sent{
			ID: row.ID, Title: row.Title, Body: row.Body, Link: row.Link, CreatedAt: row.CreatedAt.UTC(),
			ExpiresAt: utc(row.ExpiresAt), WithdrawnAt: utc(row.WithdrawnAt),
			RecipientCount: row.RecipientCount, ReadCount: row.ReadCount,
			Status: statusAt(row.WithdrawnAt, row.ExpiresAt, now),
		})
	}
	x.send(http.StatusOK, map[string]any{"announcements": listed})
}

func (r Routes) send(x *exchange) {
	raw, err := io.ReadAll(io.LimitReader(x.ctx.BodyReader(), maxBodyBytes+1))
	if err != nil {
		x.refuse(http.StatusBadRequest, "The request body could not be read.")
		return
	}
	if len(raw) > maxBodyBytes {
		x.refuse(http.StatusRequestEntityTooLarge, "The announcement is too large.")
		return
	}
	now := r.now()
	checked, failures := readDraft(raw, now)
	if len(failures) > 0 {
		r.Errors.WriteValidation(x.response, x.requestID, failures)
		return
	}
	params := SendAnnouncementParams{
		Title: checked.Title, Body: checked.Body, Link: checked.Link,
		SenderID: x.userID, ExpiresAt: checked.ExpiresAt, Now: now,
	}
	if checked.ExpiresAt != nil {
		// The copy's expiry, as the inbox reads it out of the metadata.
		text := checked.ExpiresAt.Format(time.RFC3339)
		params.ExpiresText = &text
	}
	row, err := New(r.Pool).SendAnnouncement(x.ctx.Context(), params)
	if err != nil {
		x.fail("could not send the announcement", err)
		return
	}
	r.Logger.InfoContext(x.ctx.Context(), "an announcement was sent",
		slog.String("request_id", x.requestID), slog.Int64("announcement_id", row.ID),
		slog.Int64("recipients", row.RecipientCount), slog.String("sender_id", x.userID))
	x.send(http.StatusCreated, sent{
		ID: row.ID, Title: row.Title, Body: row.Body, Link: row.Link, CreatedAt: row.CreatedAt.UTC(),
		ExpiresAt: utc(row.ExpiresAt), RecipientCount: row.RecipientCount,
		Status: statusAt(nil, row.ExpiresAt, now),
	})
}

func (r Routes) withdraw(x *exchange) {
	id, err := strconv.ParseInt(x.ctx.Param("announcement_id"), 10, 64)
	if err != nil || id < 1 {
		r.Errors.WriteValidation(x.response, x.requestID, []errorenvelope.Failure{{
			Location: []string{"path", "announcement_id"}, Message: "Input should be a valid integer"}})
		return
	}
	now := r.now()
	row, err := New(r.Pool).WithdrawAnnouncement(x.ctx.Context(), WithdrawAnnouncementParams{ID: id, Now: now})
	if errors.Is(err, pgx.ErrNoRows) {
		x.refuse(http.StatusNotFound, announcementAbsent)
		return
	}
	if err != nil {
		x.fail("could not withdraw the announcement", err)
		return
	}
	r.Logger.InfoContext(x.ctx.Context(), "an announcement was withdrawn",
		slog.String("request_id", x.requestID), slog.Int64("announcement_id", row.ID), slog.String("by", x.userID))
	x.send(http.StatusOK, sent{
		ID: row.ID, Title: row.Title, Body: row.Body, Link: row.Link, CreatedAt: row.CreatedAt.UTC(),
		ExpiresAt: utc(row.ExpiresAt), WithdrawnAt: utc(row.WithdrawnAt),
		RecipientCount: row.RecipientCount, ReadCount: row.ReadCount,
		Status: statusAt(row.WithdrawnAt, row.ExpiresAt, now),
	})
}

// utc renders a stored time in UTC, so every time on the page ends in Z
// whatever zone the database session is in.
func utc(moment *time.Time) *time.Time {
	if moment == nil {
		return nil
	}
	converted := moment.UTC()
	return &converted
}
