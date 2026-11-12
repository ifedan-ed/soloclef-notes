// Package e2eseed serves the routes the web app's end-to-end suite sets its
// scene with: a session for the seeded account without the sign-in limit,
// finished studio outputs without the sandbox or a paid model, a source
// with a real PDF, inbox rows, an expired share link, and the last mail the
// stack "sent".
//
//	POST /__e2e__/auth/token                              a session token for an account
//	POST /__e2e__/notifications                           rows in an account's inbox
//	POST /__e2e__/public-links/{snapshot_id}/expire       make an output's or chat's link expired
//	POST /__e2e__/public-links/notebook/{link_id}/expire  make a notebook's link expired
//	GET  /__e2e__/mail/last?to=                           the newest mail to an address
//	POST /__e2e__/seed/studio-output                      a studio output: ready, running or failed
//	POST /__e2e__/seed/complete-output                    finish an output the app queued
//	POST /__e2e__/seed/source-pdf                         a PDF source with a chunk on page 2
//
// # They are never in production
//
// Every one of these hands out something a product route guards. So:
//
//   - They are mounted only when SOLOCLEF_API_E2E_ROUTES is exactly "true"
//     (cmd/api), and the production compose file writes "false" as a
//     literal, not as a variable an .env could set.
//   - Mounted, each still refuses a request that does not carry the
//     stack's seed secret, compared in constant time. The secret has no
//     default: a stack that switches the routes on names its own.
//   - They are not in the served-route list, so they are outside the rate
//     limiter (which is the point of the token route) and outside the
//     parity inventory: they are not routes of the product.
//
// The Python tier kept its seed routes in a tests tree that its image left
// out. One Go binary serves both, so the switch and the secret do that job.
//
// # What a seed writes
//
// Rows shaped as the product writes them, under the owner of the notebook
// or address the request names, and files in the stack's own file store
// under the keys the product uses. The viewers under test then read a
// seeded output exactly as they read a generated one. The files are fixed
// fixtures compiled in (fixtures/); the Python built them on each request
// with ffmpeg and four document libraries, which the API image does not
// carry.
package e2eseed

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// The paths.
const (
	TokenPath              = "/__e2e__/auth/token"
	NotificationsPath      = "/__e2e__/notifications"
	ExpireSnapshotPath     = "/__e2e__/public-links/{snapshot_id}/expire"
	ExpireNotebookLinkPath = "/__e2e__/public-links/notebook/{link_id}/expire"
	LastMailPath           = "/__e2e__/mail/last"
	SeedOutputPath         = "/__e2e__/seed/studio-output"
	CompleteOutputPath     = "/__e2e__/seed/complete-output"
	SeedSourcePath         = "/__e2e__/seed/source-pdf"
)

// SecretHeader carries the stack's seed secret.
const SecretHeader = "X-E2E-Mint-Secret"

// minSecretChars keeps a stack from switching the routes on behind a
// secret short enough to guess.
const minSecretChars = 16

// maxBodyBytes bounds a seed request. The largest is a report's Markdown
// or an interactive report's page.
const maxBodyBytes = 1 << 20

// Routes is the eight routes.
type Routes struct {
	Pool *pgxpool.Pool
	// Files is the stack's own file store, written as the product writes it.
	Files filestore.Writer
	// Secret is what every request must carry in SecretHeader.
	Secret string
	// MintSession signs an account in and answers the access token the gate
	// verifies (signin.MintSession under the session secret): a real
	// sign-in, because the gate refuses an access token whose sign-in it
	// cannot find.
	MintSession func(ctx context.Context, userID string, now time.Time) (string, error)
	// Outbox is where this stack's mail goes instead of out (cmd/api hands
	// the same one to the sign-in routes as their mailer).
	Outbox *Outbox
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

// Check refuses a set of seed routes that is not whole. Called once, at
// start-up, and only when the routes are switched on.
func (r Routes) Check() error {
	switch {
	case r.Pool == nil:
		return errors.New("e2eseed: no database")
	case r.Files == nil:
		return errors.New("e2eseed: no file store to seed files into")
	case len(r.Secret) < minSecretChars:
		return errors.New("e2eseed: the seed secret must be at least " + strconv.Itoa(minSecretChars) + " characters; the routes are not mounted without one")
	case r.MintSession == nil:
		return errors.New("e2eseed: nothing to mint a session token with")
	case r.Outbox == nil:
		return errors.New("e2eseed: no outbox for the stack's mail")
	case r.Logger == nil:
		return errors.New("e2eseed: no logger")
	}
	return nil
}

// Public is every route as a "METHOD /path" pair. None takes a session:
// the seed secret is the whole of the authorisation.
func Public() []string {
	return []string{
		http.MethodPost + " " + TokenPath,
		http.MethodPost + " " + NotificationsPath,
		http.MethodPost + " " + ExpireSnapshotPath,
		http.MethodPost + " " + ExpireNotebookLinkPath,
		http.MethodGet + " " + LastMailPath,
		http.MethodPost + " " + SeedOutputPath,
		http.MethodPost + " " + CompleteOutputPath,
		http.MethodPost + " " + SeedSourcePath,
	}
}

// Register mounts the routes and says so, loudly: a line an operator who
// did not mean to switch them on will see.
func (r Routes) Register(api huma.API) {
	mount[struct{}](api, r, http.MethodPost, TokenPath, "e2e-token", r.token)
	mount[struct{}](api, r, http.MethodPost, NotificationsPath, "e2e-notifications", r.notifications)
	mount[struct {
		SnapshotID string `path:"snapshot_id"`
	}](api, r, http.MethodPost, ExpireSnapshotPath, "e2e-expire-snapshot-link", r.expireSnapshot)
	mount[struct {
		LinkID string `path:"link_id"`
	}](api, r, http.MethodPost, ExpireNotebookLinkPath, "e2e-expire-notebook-link", r.expireNotebookLink)
	mount[struct{}](api, r, http.MethodGet, LastMailPath, "e2e-last-mail", r.lastMail)
	mount[struct{}](api, r, http.MethodPost, SeedOutputPath, "e2e-seed-output", r.seedOutput)
	mount[struct{}](api, r, http.MethodPost, CompleteOutputPath, "e2e-complete-output", r.completeOutput)
	mount[struct{}](api, r, http.MethodPost, SeedSourcePath, "e2e-seed-source", r.seedSource)
	r.Logger.Warn("the end-to-end test routes are mounted under /__e2e__: this stack hands out sessions and seeds rows to anyone holding its seed secret, and must not be a production one")
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

// exchange is one request that carried the secret.
type exchange struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	routes    Routes
}

func mount[P any](api huma.API, r Routes, method, path, id string, serve func(*exchange)) {
	huma.Register(api, huma.Operation{
		OperationID: id, Method: method, Path: path, Summary: "End-to-end test support", Tags: []string{"__e2e__"},
		// Never in the published API description, mounted or not.
		Hidden: true,
	}, func(_ context.Context, _ *P) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			x := &exchange{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), routes: r}
			// Constant time, so the secret cannot be found a character
			// at a time by how long a refusal takes.
			if subtle.ConstantTimeCompare([]byte(ctx.Header(SecretHeader)), []byte(r.Secret)) != 1 {
				x.refuse(http.StatusForbidden, "invalid e2e secret")
				return
			}
			serve(x)
		}}, nil
	})
}

func (x *exchange) refuse(status int, message string) {
	x.routes.Errors.Write(x.response, status, message, x.requestID)
}

func (x *exchange) send(status int, value any) {
	x.response.Header().Set("Content-Type", "application/json")
	x.response.Header().Set("Cache-Control", "no-store")
	x.response.WriteHeader(status)
	_ = json.NewEncoder(x.response).Encode(value)
}

func (x *exchange) fail(reason string, cause error) {
	attributes := []any{slog.String("request_id", x.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	x.routes.Logger.ErrorContext(x.ctx.Context(), "an end-to-end seed route could not be served", attributes...)
	// The cause is said, unlike on a product route: the reader is a test
	// run's log, and "something went wrong" would cost its author an hour.
	message := reason
	if cause != nil {
		message += ": " + cause.Error()
	}
	x.refuse(http.StatusInternalServerError, message)
}

// body reads a request's JSON into value, or answers the refusal.
func (x *exchange) body(value any) bool {
	raw, err := io.ReadAll(io.LimitReader(x.ctx.BodyReader(), maxBodyBytes+1))
	if err != nil || len(raw) > maxBodyBytes {
		x.refuse(http.StatusRequestEntityTooLarge, "the seed request could not be read or is over 1 MiB")
		return false
	}
	if err := json.Unmarshal(raw, value); err != nil {
		x.refuse(http.StatusUnprocessableEntity, "the seed request is not the JSON this route reads: "+err.Error())
		return false
	}
	return true
}

// id reads a path's whole number, or answers the refusal.
func (x *exchange) id(name string) (int64, bool) {
	value, err := strconv.ParseInt(x.ctx.Param(name), 10, 64)
	if err != nil || value < 1 {
		x.refuse(http.StatusUnprocessableEntity, name+" must be a whole number")
		return 0, false
	}
	return value, true
}
