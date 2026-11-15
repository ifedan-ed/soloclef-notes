// Package presencebeat is the heartbeat that says a person is looking at
// Soloclef right now:
//
//	PUT /api/v1/presence   {"client_id": "...", "active": true|false}   204
//
// A visible web tab, or the phone app in the foreground, sends active:true
// as it becomes visible and every minute while it stays so, and
// active:false the moment it is hidden. While any of the person's clients
// has a live mark, the worker pushes nothing to their phone
// (soloclef_notify/delivery); the inbox row is written as ever.
//
// Why this is the lightest signal that works is in soloclef_notify/presence,
// which spells the keys this route writes and the worker reads. In short:
// one small request a minute per visible client, one sorted-set member in
// Valkey and no Postgres row, and nothing at all from a hidden tab.
//
// Session only: presence is a person at a screen, and an API key or an
// agent is never one.
package presencebeat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_notify/presence"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Path is the route.
const Path = "/api/v1/presence"

// Served is the route as a "METHOD /path" pair.
func Served() []string { return []string{http.MethodPut + " " + Path} }

// bodyMax bounds the body: an id and a switch.
const bodyMax = 1 << 10

// Marks is where the marks are kept (Valkey).
type Marks interface {
	// Mark records the client as looking until expiry.
	Mark(ctx context.Context, userID, clientID string, now time.Time) error
	// Unmark removes the client's mark.
	Unmark(ctx context.Context, userID, clientID string) error
}

// Route serves the heartbeat.
type Route struct {
	Marks  Marks
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

func (r Route) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "presence-heartbeat", Method: http.MethodPut, Path: Path,
		Summary: "Say whether this tab or app is in front of the person", Tags: []string{"push"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		r.fail(ctx, "the presence route ran without a caller", nil)
		return
	}
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, "This needs a signed-in session, not an API key.", requestID)
		return
	}
	body, err := io.ReadAll(io.LimitReader(ctx.BodyReader(), bodyMax+1))
	if err != nil || len(body) > bodyMax {
		r.Errors.Write(response, http.StatusRequestEntityTooLarge, "The heartbeat is too large.", requestID)
		return
	}
	var beat struct {
		ClientID string `json:"client_id"`
		Active   *bool  `json:"active"`
	}
	switch {
	case json.Unmarshal(body, &beat) != nil || !presence.ValidClientID(beat.ClientID):
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{
			Location: []string{"body", "client_id"}, Message: "Input should be 8 to 64 letters, digits, - or _"}})
		return
	case beat.Active == nil:
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{{
			Location: []string{"body", "active"}, Message: "Input should be true or false"}})
		return
	}
	if *beat.Active {
		err = r.Marks.Mark(ctx.Context(), caller.UserID, beat.ClientID, r.now())
	} else {
		err = r.Marks.Unmark(ctx.Context(), caller.UserID, beat.ClientID)
	}
	if err != nil {
		// A 503, not a 500: the mark is a hint, the client ignores the
		// failure, and the next beat tries again.
		if !errors.Is(err, context.Canceled) {
			r.Logger.WarnContext(ctx.Context(), "a presence mark could not be written",
				slog.String("request_id", requestID), slog.String("error", err.Error()))
		}
		r.Errors.Write(response, http.StatusServiceUnavailable, "Presence is unavailable right now.", requestID)
		return
	}
	ctx.SetHeader("Cache-Control", "no-store")
	ctx.SetStatus(http.StatusNoContent)
}

func (r Route) fail(ctx huma.Context, reason string, cause error) {
	requestID := requestcontext.ID(ctx.Context())
	r.Logger.ErrorContext(ctx.Context(), "the presence route could not be served",
		slog.String("request_id", requestID), slog.String("reason", reason), slog.Any("error", cause))
	r.Errors.Write(humawriter.New(ctx), http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
