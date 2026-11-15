package livechanges

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/rollingdeadline"
)

// Path is the route.
const Path = "/api/v1/events"

// Served is the route as a "METHOD /path" pair.
func Served() []string { return []string{http.MethodGet + " " + Path} }

// The stream's timing. Properties of the protocol, not of a deployment.
const (
	// Heartbeat is how often an idle stream says it is alive: well inside
	// the 60 seconds a client waits before calling the stream dead, and
	// inside every proxy's idle timeout on the way (Caddy's, a phone
	// carrier's NAT), which would otherwise cut a quiet connection.
	Heartbeat = 25 * time.Second
	// MaxLife ends every stream after this long, and the client reconnects
	// at once (resuming, so it reloads nothing). The credential is checked
	// at connect only, and an access token lives an hour: a signed-out
	// session must not keep hearing about an account for longer than this.
	// It also spreads streams again across API replicas after a deploy.
	MaxLife = 10 * time.Minute
)

// Route serves the stream: GET /api/v1/events.
//
// Session only, as presence is: a stream is a person's open screen, and an
// API key or an agent has no screen to refresh.
type Route struct {
	Hub *Hub
	// Lifetime is the process's: when it ends (a shutdown) every stream
	// ends with it, so a graceful shutdown is not held open by streams that
	// never finish on their own. The clients reconnect to a replica that is
	// still up.
	Lifetime context.Context
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
	// Stall is how long a client may go without taking a frame before the
	// connection is dropped (cmd/api's writeTimeout figure).
	Stall time.Duration
	// Heartbeat and MaxLife are the constants above; only a test names others.
	Heartbeat time.Duration
	MaxLife   time.Duration
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "live-changes", Method: http.MethodGet, Path: Path,
		Summary: "Hear when something this account can see has changed",
		Description: "A Server-Sent Events stream. The first frame is " +
			`{"type":"ready","resumed":bool}; each later frame names a kind of thing that changed ` +
			"(and sometimes which), never its contents. A `: keepalive` comment comes every 25 seconds.",
		Tags: []string{"live"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		r.Logger.ErrorContext(ctx.Context(), "the live changes route ran without a caller",
			slog.String("request_id", requestID))
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, "This needs a signed-in session, not an API key.", requestID)
		return
	}
	// Before the status, so a connection whose deadline cannot be moved is
	// still answered with an error (rollingdeadline.Wrap).
	steady, err := rollingdeadline.Wrap(response, r.Stall)
	if err != nil {
		r.Logger.ErrorContext(ctx.Context(), "the live changes stream cannot outlive the write timeout",
			slog.String("request_id", requestID), slog.String("error", err.Error()))
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}
	flush := http.NewResponseController(steady).Flush

	sub, replay, resumed := r.Hub.Subscribe(caller.UserID, ctx.Header("Last-Event-ID"))
	defer sub.Close()

	header := response.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-store")
	// Tells nginx-style proxies not to buffer the stream.
	header.Set("X-Accel-Buffering", "no")
	response.WriteHeader(http.StatusOK)

	if writeReady(steady, resumed) != nil {
		return
	}
	for _, frame := range replay {
		if writeFrame(steady, frame) != nil {
			return
		}
	}
	if flush() != nil {
		return
	}

	heartbeat := time.NewTicker(r.Heartbeat)
	defer heartbeat.Stop()
	life := time.NewTimer(r.MaxLife)
	defer life.Stop()
	for {
		select {
		case <-ctx.Context().Done():
			return
		case <-r.Lifetime.Done():
			return
		case <-sub.Dropped():
			return
		case <-life.C:
			return
		case <-heartbeat.C:
			if _, err := io.WriteString(steady, ": keepalive\n\n"); err != nil {
				return
			}
		case frame := <-sub.Frames():
			if writeFrame(steady, frame) != nil {
				return
			}
			// Write what has queued up behind it before flushing once: a
			// burst of indexing changes is one packet, not fifty.
			if drainQueued(steady, sub) != nil {
				return
			}
		}
		if flush() != nil {
			return
		}
	}
}

func drainQueued(w io.Writer, sub *Subscription) error {
	for {
		select {
		case frame := <-sub.Frames():
			if err := writeFrame(w, frame); err != nil {
				return err
			}
		default:
			return nil
		}
	}
}

func writeReady(w io.Writer, resumed bool) error {
	body, err := json.Marshal(struct {
		Type    string `json:"type"`
		Resumed bool   `json:"resumed"`
	}{"ready", resumed})
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "data: "+string(body)+"\n\n")
	return err
}

// writeFrame writes one change. JSON has no raw newline in it, so one data
// line always holds the whole change.
func writeFrame(w io.Writer, frame Frame) error {
	body, err := json.Marshal(frame.Change)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, "id: "+frame.ID+"\ndata: "+string(body)+"\n\n")
	return err
}
