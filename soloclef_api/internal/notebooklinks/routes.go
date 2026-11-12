package notebooklinks

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// Routes is every route of the package and what they share.
type Routes struct {
	Pool *pgxpool.Pool
	// Files is every store this process reads; a file's row says which one
	// holds it.
	Files *filestore.Stores
	// StallTimeout is how long a visitor may accept nothing before a stream
	// is dropped; however long a download takes otherwise is theirs.
	StallTimeout time.Duration
	// FrontendURL is the web app's address, which a link's public URL
	// starts with.
	FrontendURL string
	Errors      errorenvelope.Writer
	Logger      *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

// Check says what the routes cannot work without, at start-up and not on
// the first request. There is no figure or address to guess for any of
// them.
func (r Routes) Check() error {
	switch {
	case r.Pool == nil:
		return errors.New("notebooklinks: Routes needs the database pool")
	case r.Files == nil:
		return errors.New("notebooklinks: Routes needs the file stores")
	case r.StallTimeout <= 0:
		return errors.New("notebooklinks: Routes needs a StallTimeout greater than zero")
	case strings.TrimSpace(r.FrontendURL) == "":
		return errors.New("notebooklinks: Routes needs the web app's address (FrontendURL), which every link's URL starts with")
	case r.Logger == nil:
		return errors.New("notebooklinks: Routes needs a logger")
	}
	return nil
}

func (r Routes) now() time.Time {
	if r.Now == nil {
		return time.Now().UTC()
	}
	return r.Now().UTC()
}

type (
	noPath       struct{}
	notebookPath struct {
		NotebookID string `path:"notebook_id"`
	}
	linkPath struct {
		LinkID string `path:"link_id"`
	}
	tokenPath struct {
		ShareToken string `path:"share_token"`
	}
	artifactPath struct {
		ShareToken string `path:"share_token"`
		ArtifactID string `path:"artifact_id"`
	}
	artifactFilePath struct {
		ShareToken string `path:"share_token"`
		ArtifactID string `path:"artifact_id"`
		FileID     string `path:"file_id"`
	}
	sourcePath struct {
		ShareToken string `path:"share_token"`
		DocumentID string `path:"document_id"`
	}
	sourceFilePath struct {
		ShareToken string `path:"share_token"`
		DocumentID string `path:"document_id"`
		FileID     string `path:"file_id"`
	}
)

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	owned[notebookPath](api, r, http.MethodGet, NotebookLinkPath, "read-notebook-public-link", "A notebook's live link", r.read)
	owned[notebookPath](api, r, http.MethodPost, NotebookLinkPath, "share-notebook", "Share a notebook by a live link", r.share)
	owned[notebookPath](api, r, http.MethodDelete, NotebookLinkPath, "stop-sharing-notebook", "Stop sharing a notebook", r.stop)
	owned[noPath](api, r, http.MethodGet, LinksPath, "list-notebook-public-links", "The account's live links", r.list)
	owned[linkPath](api, r, http.MethodDelete, LinkPath, "revoke-notebook-public-link", "Stop one live link", r.revoke)

	visited[tokenPath](api, r, PagePath, "read-public-notebook", "A shared notebook as it is now", r.page)
	visited[artifactPath](api, r, ManifestPath, "read-public-notebook-artifact", "How a shared output opens", r.manifest)
	visited[artifactFilePath](api, r, ArtifactFilePath, "read-public-notebook-artifact-file", "A shared output's file", r.artifactFile)
	visited[artifactPath](api, r, ArtifactSavePath, "download-public-notebook-artifact", "A shared output, to save", r.artifactSave)
	visited[sourcePath](api, r, SourcePath, "read-public-notebook-source", "How a shared source opens", r.source)
	visited[sourceFilePath](api, r, SourceFilePath, "read-public-notebook-source-file", "A shared source's original file", r.sourceFile)
}

// exchange is one request being answered. userID is the caller's on an
// owner's route; on a visitor's it stays empty until the link is resolved,
// and is then the link's owner, never anything the request said.
type exchange struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	userID    string
	routes    Routes
}

func begin(ctx huma.Context, r Routes) *exchange {
	return &exchange{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), routes: r}
}

// owned mounts a route that acts for the signed-in caller.
func owned[P any](api huma.API, r Routes, method, path, id, summary string, serve func(*exchange)) {
	huma.Register(api, huma.Operation{
		OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"public-sharing"},
	}, func(_ context.Context, _ *P) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			x := begin(ctx, r)
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

// visited mounts a route that takes no credential. It reads no caller on
// purpose: a signed-in person opening a link is a visitor like any other,
// and must be shown exactly what a stranger is.
func visited[P any](api huma.API, r Routes, path, id, summary string, serve func(*exchange, shared)) {
	huma.Register(api, huma.Operation{
		OperationID: id, Method: http.MethodGet, Path: path, Summary: summary, Tags: []string{"public-sharing"},
	}, func(_ context.Context, _ *P) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			x := begin(ctx, r)
			visitorHeaders(x.response.Header())
			if link, ok := x.resolve(); ok {
				serve(x, link)
			}
		}}, nil
	})
}

// visitorHeaders are on every answer to a visitor, refusals included. The
// token is in the page's URL, so no referrer is sent onward; nothing here
// is for a search engine; and no browser guesses a type for what it was
// sent.
func visitorHeaders(headers http.Header) {
	headers.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
	headers.Set("Referrer-Policy", "no-referrer")
	headers.Set("X-Content-Type-Options", "nosniff")
}

// id reads one path parameter as a row id for an owner's route: a value
// that is not a whole number is a 422. One too wide for a bigint names no
// row and reads as 0, which no row has.
func (x *exchange) id(name string) (int64, bool) {
	text := x.ctx.Param(name)
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		if !digits(text) {
			x.routes.Errors.WriteValidation(x.response, x.requestID, []errorenvelope.Failure{{
				Location: []string{"path", name}, Message: "Input should be a valid integer"}})
			return 0, false
		}
		value = 0
	}
	return value, true
}

// visitorID reads one path parameter as a row id for a visitor's route.
// Anything that is not an id some row could have reads as 0 and so finds
// nothing: a visitor is told "Not found" and never what was wrong.
func (x *exchange) visitorID(name string) int64 {
	text := x.ctx.Param(name)
	if !digits(text) {
		return 0
	}
	value, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func digits(text string) bool {
	if text == "" {
		return false
	}
	for _, c := range text {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func (x *exchange) refuse(status int, message string) {
	x.routes.Errors.Write(x.response, status, message, x.requestID)
}

// send answers JSON. Nothing here is cached: an owner's answer changes with
// a press of a button, and a visitor's must stop the moment the link does.
func (x *exchange) send(status int, cacheControl string, value any) {
	x.response.Header().Set("Content-Type", "application/json")
	x.response.Header().Set("Cache-Control", cacheControl)
	x.response.WriteHeader(status)
	_ = json.NewEncoder(x.response).Encode(value)
}

// fail logs the cause and answers the 500. A cancelled request is not a
// fault and is not logged as one.
func (x *exchange) fail(reason string, cause error) {
	if !errors.Is(cause, context.Canceled) {
		attributes := []any{slog.String("request_id", x.requestID), slog.String("reason", reason)}
		if cause != nil {
			attributes = append(attributes, slog.String("error", cause.Error()))
		}
		x.routes.Logger.ErrorContext(x.ctx.Context(), "a notebook link route could not be served", attributes...)
	}
	x.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}
