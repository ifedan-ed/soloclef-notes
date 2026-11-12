// Package clienterrors serves POST /api/v1/client-errors: the web app's
// crash reports, so that a "Something went wrong" page leaves a line an
// operator can find, with the page it happened on and the build it came
// from.
//
// Nothing is stored. The report becomes one warning in this process's log
// and that line is the whole product.
//
// # What keeps it from being misused
//
//   - A browser session only. An API key or an agent has no page to crash.
//   - A body of at most 8 KiB, refused before it is parsed.
//   - A budget per account and one for the whole site, each per minute, so
//     neither a looping page nor a crowd of them can fill the log.
//   - The line carries no page content: the path is cut at "?" and "#",
//     which can hold tokens, and every text is cut to a fixed length.
//
// # What it does better than the Python
//
//   - **The budgets are shared by every replica.** The Python counted in
//     its own process's memory, so each worker allowed the whole budget
//     again. These are counted where the request limiter counts.
//   - **A report cannot forge a log line.** The Python flattened the text
//     and indented the stack to that end; here the line is structured, so
//     a newline in a message is data inside one field whatever it says.
package clienterrors

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Path is the route.
const Path = "/api/v1/client-errors"

// Served is the route as a "METHOD /path" pair.
func Served() []string { return []string{http.MethodPost + " " + Path} }

// The limits. The web app sends at most five reports per page load and
// never the same one twice, so ten a minute is room for an honest browser
// and no more.
const (
	maxBodyBytes  = 8 << 10
	perAccount    = 10
	acrossTheSite = 120
	window        = time.Minute

	stackLines    = 12
	stackLineMax  = 300
	messageMax    = 500
	pathMax       = 300
	shortFieldMax = 100
	buildMax      = 64
)

// kinds is where in the web app a report can come from.
var kinds = []string{"error-boundary", "global-error", "window-error", "unhandled-rejection"}

// Counter counts hits in a window and answers the count including this
// one (ratelimit.Storage, the request limiter's own counter).
type Counter interface {
	Hit(ctx context.Context, key string, window time.Duration, cost int64) (int64, error)
}

// Route serves the report.
type Route struct {
	Budget Counter
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Check refuses a route that could not count: a report route with no
// budget is an open door to the log.
func (r Route) Check() error {
	if r.Budget == nil || r.Logger == nil {
		return errors.New("clienterrors: the route needs a counter for its budgets and a logger to write the reports to")
	}
	return nil
}

// Register mounts the route.
func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "report-client-error", Method: http.MethodPost, Path: Path,
		Summary: "Report a crash of the web app", Tags: []string{"client-errors"},
	}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

// report is what the web app sends (lib/client-error-report.ts).
type report struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Message   string `json:"message"`
	Stack     string `json:"stack"`
	Path      string `json:"path"`
	Digest    string `json:"digest"`
	Build     string `json:"build"`
	RequestID string `json:"request_id"`
}

func (r Route) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	caller, ok := principal.FromContext(ctx.Context())
	if !ok {
		r.Logger.ErrorContext(ctx.Context(), "the client error route ran without a caller", slog.String("request_id", requestID))
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
		return
	}
	if caller.Method != principal.MethodSession {
		r.Errors.Write(response, http.StatusForbidden, "Crash reports are taken from a signed-in browser only.", requestID)
		return
	}
	// One byte past the cap is read so that "too large" can be told from
	// "exactly the cap"; nothing past that is ever held.
	raw, err := io.ReadAll(io.LimitReader(ctx.BodyReader(), maxBodyBytes+1))
	if err != nil {
		r.Errors.Write(response, http.StatusBadRequest, "The report could not be read.", requestID)
		return
	}
	if len(raw) > maxBodyBytes {
		r.Errors.Write(response, http.StatusRequestEntityTooLarge, "Report too large", requestID)
		return
	}
	// The account's budget first: an account over its own does not spend
	// the site's, so one looping page cannot silence everyone else's
	// reports. An unreadable report is counted too; it is the same abuse.
	for _, budget := range []struct {
		key   string
		limit int64
	}{{"client-errors:account:" + caller.UserID, perAccount}, {"client-errors:site", acrossTheSite}} {
		count, err := r.Budget.Hit(ctx.Context(), budget.key, window, 1)
		if err != nil {
			// A report is not worth serving without its budget.
			r.Logger.ErrorContext(ctx.Context(), "the client error budget could not be counted",
				slog.String("request_id", requestID), slog.String("error", err.Error()))
			r.Errors.Write(response, http.StatusServiceUnavailable, "Crash reports cannot be taken right now.", requestID)
			return
		}
		if count > budget.limit {
			response.Header().Set("Retry-After", "60")
			r.Errors.Write(response, http.StatusTooManyRequests, "Too many crash reports. Try again in a minute.", requestID)
			return
		}
	}
	var sent report
	if err := json.Unmarshal(raw, &sent); err != nil || !slices.Contains(kinds, sent.Kind) {
		r.Errors.Write(response, http.StatusUnprocessableEntity, "Invalid report", requestID)
		return
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelWarn, "client error", lineOf(sent, caller.UserID, requestID)...)
	response.WriteHeader(http.StatusNoContent)
}

// lineOf is the report as the log holds it: every text cut to its length,
// the path without its query, the stack as its first few frames.
func lineOf(sent report, userID, requestID string) []slog.Attr {
	path, _, _ := strings.Cut(sent.Path, "?")
	path, _, _ = strings.Cut(path, "#")
	line := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("user_id", userID),
		slog.String("kind", sent.Kind),
		slog.String("path", flat(path, pathMax)),
		slog.String("build", flat(sent.Build, buildMax)),
		slog.String("name", flat(sent.Name, shortFieldMax)),
		slog.String("message", flat(sent.Message, messageMax)),
	}
	if digest := flat(sent.Digest, shortFieldMax); digest != "" {
		line = append(line, slog.String("digest", digest))
	}
	// The id of the API request that failed, when the crash was one: it
	// is how this line is joined to that request's own.
	if failed := flat(sent.RequestID, buildMax); failed != "" {
		line = append(line, slog.String("failed_request", failed))
	}
	if frames := stackExcerpt(sent.Stack); len(frames) > 0 {
		line = append(line, slog.Any("stack", frames))
	}
	return line
}

// flat is a text on one line, cut to at most limit characters.
func flat(text string, limit int) string {
	joined := strings.Join(strings.Fields(text), " ")
	if runes := []rune(joined); len(runes) > limit {
		return string(runes[:limit])
	}
	return joined
}

// stackExcerpt is the first frames of a stack: enough to find the code,
// and a bound on what one report can write.
func stackExcerpt(stack string) []string {
	var frames []string
	for _, line := range strings.Split(stack, "\n") {
		if line = flat(line, stackLineMax); line != "" {
			frames = append(frames, line)
			if len(frames) == stackLines {
				break
			}
		}
	}
	return frames
}
