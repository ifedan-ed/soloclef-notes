package publicshare

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

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/artifacts"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/routeanalytics"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// Routes is the eight routes and what they share.
type Routes struct {
	Pool *pgxpool.Pool
	// Files is every store this process reads; a file's row says which one
	// holds it.
	Files *filestore.Stores
	// StallTimeout is how long a client may accept nothing before a stream
	// is dropped. Required: there is no figure to guess that would not
	// silently disagree with the server's own write timeout.
	StallTimeout time.Duration
	// FrontendURL is where the web app is served, which a new link's
	// address begins with.
	FrontendURL string
	// Analytics records a copy, the one share-link conversion only the
	// server can see. Its zero value records nothing.
	Analytics routeanalytics.Capture
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
	// Now is nil for the system clock; only a test names another. It is
	// the moment a link's expiry is measured against.
	Now func() time.Time
}

// Check refuses a deployment that left out something a route cannot run
// without, at start-up and not on the first request.
func (r Routes) Check() error {
	switch {
	case r.Pool == nil:
		return errors.New("publicshare: Routes needs the database pool")
	case r.Files == nil:
		return errors.New("publicshare: Routes needs the file stores")
	case r.StallTimeout <= 0:
		return errors.New("publicshare: Routes needs a StallTimeout greater than zero")
	case r.FrontendURL == "":
		return errors.New("publicshare: Routes needs the web app's address, which a link's address begins with")
	case r.Logger == nil:
		return errors.New("publicshare: Routes needs a logger")
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
	artifactPath struct {
		ShareToken string `path:"share_token"`
		ArtifactID string `path:"artifact_id"`
	}
	filePath struct {
		ShareToken string `path:"share_token"`
		ArtifactID string `path:"artifact_id"`
		FileID     string `path:"file_id"`
	}
	slidePath struct {
		ShareToken  string `path:"share_token"`
		ArtifactID  string `path:"artifact_id"`
		SlideNumber string `path:"slide_number"`
	}
	podcastPath struct {
		ShareToken string `path:"share_token"`
		PodcastID  string `path:"podcast_id"`
	}
	presentationPath struct {
		ShareToken     string `path:"share_token"`
		PresentationID string `path:"video_presentation_id"`
	}
	presentationSlidePath struct {
		ShareToken     string `path:"share_token"`
		PresentationID string `path:"video_presentation_id"`
		SlideNumber    string `path:"slide_number"`
	}
	tokenPath struct {
		ShareToken string `path:"share_token"`
	}
	outputPath struct {
		NotebookID string `path:"notebook_id"`
		OutputID   string `path:"output_id"`
	}
)

// Register mounts the routes. It panics on a deployment Check refuses, so
// a caller that skipped Check still cannot serve a half-built route.
func (r Routes) Register(api huma.API) {
	if err := r.Check(); err != nil {
		panic(err)
	}
	mount[artifactPath](api, r, http.MethodGet, ContentPath, "read-public-artifact-content", "A shared artifact's main file", r.content)
	mount[filePath](api, r, http.MethodGet, FilePath, "read-public-artifact-file", "One file of a shared artifact", r.file)
	mount[slidePath](api, r, http.MethodGet, SlideAudioPath, "read-public-artifact-slide-audio", "A shared video slide's narration", r.slideAudio)
	mount[podcastPath](api, r, http.MethodGet, PodcastStreamPath, "stream-public-podcast", "A shared podcast's audio", r.podcastStream)
	mount[presentationPath](api, r, http.MethodGet, PresentationPath, "read-public-video-presentation", "A presentation frozen into a link", r.presentation)
	mount[presentationSlidePath](api, r, http.MethodGet, PresentationAudioPath, "read-public-video-presentation-slide-audio", "A frozen presentation's slide narration", r.presentationAudio)
	mount[tokenPath](api, r, http.MethodPost, ClonePath, "clone-public-chat", "Copy a shared chat into the caller's account", r.clone)
	mount[outputPath](api, r, http.MethodPost, OutputLinkPath, "share-studio-output", "Make a public link to a finished studio output", r.shareOutput)
}

// exchange is one request being answered. It carries no caller: six of the
// routes have none, and the two that do ask for one themselves (signedIn).
type exchange struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	routes    Routes
}

func mount[P any](api huma.API, r Routes, method, path, id, summary string, serve func(*exchange)) {
	huma.Register(api, huma.Operation{
		OperationID: id, Method: method, Path: path, Summary: summary, Tags: []string{"public"},
	}, func(_ context.Context, _ *P) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			serve(&exchange{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context()), routes: r})
		}}, nil
	})
}

// signedIn is the caller of one of the two gated routes.
func (x *exchange) signedIn() (principal.Principal, bool) {
	caller, ok := principal.FromContext(x.ctx.Context())
	if !ok {
		x.fail("the route ran without a caller", nil)
	}
	return caller, ok
}

// ids reads the named path parameters as whole numbers, refusing every one
// that is not in a single 422. One too wide for a bigint names no row: it
// reads as 0, which no row has, and the route answers its 404.
func (x *exchange) ids(names ...string) ([]int64, bool) {
	values := make([]int64, 0, len(names))
	var failures []errorenvelope.Failure
	for _, name := range names {
		text := x.ctx.Param(name)
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			value = 0
			if !digits(text) {
				failures = append(failures, errorenvelope.Failure{
					Location: []string{"path", name}, Message: "Input should be a valid integer"})
			}
		}
		values = append(values, value)
	}
	if len(failures) > 0 {
		x.routes.Errors.WriteValidation(x.response, x.requestID, failures)
		return nil, false
	}
	return values, true
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

// send answers JSON that is never kept: each answer here depends on a link
// that can be revoked or on who asked.
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
		x.routes.Logger.ErrorContext(x.ctx.Context(), "a public share route could not be served", attributes...)
	}
	x.refuse(http.StatusInternalServerError, errorenvelope.GenericServerMessage)
}

// stream sends one stored object the way every artifact stream is sent:
// Range, validators and the rolling write deadline are internal/artifacts'
// and objecthttp's, so a public viewer's player seeks exactly as the
// owner's does.
func (x *exchange) stream(object artifacts.StoredObject) {
	artifacts.Gate{Errors: x.routes.Errors, Logger: x.routes.Logger}.
		StreamStored(x.ctx, x.response, x.requestID, x.routes.Files, x.routes.StallTimeout, object)
}

// publicURL is where a link opens.
func (r Routes) publicURL(token string) string {
	return strings.TrimRight(r.FrontendURL, "/") + "/public/" + token
}
