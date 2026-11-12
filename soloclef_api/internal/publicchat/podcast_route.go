package publicchat

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/starlettepath"
)

// PodcastRoute serves a shared podcast's title and transcript.
//
// It reads one row and no other: everything it answers was copied into the
// snapshot when the link was made, so the podcast itself is never looked up.
type PodcastRoute struct {
	Rows   Rows
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is the clock a link's expiry is measured against; nil is the
	// system's.
	Now func() time.Time
}

// Register adds the operation. The parameters are declared as strings so
// that Huma does not coerce them: FastAPI's reading of an integer is
// internal/requestnumber's to mirror, and its 422 is this tier's envelope.
func (r PodcastRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-public-podcast",
		Method:      http.MethodGet,
		Path:        PodcastPath,
		Summary:     "A podcast shared by a public link",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/public/{share_token}/podcasts/{podcast_id}, " +
			"byte for byte. Answers without a credential.",
		Tags: []string{"public"},
	}, func(ctx context.Context, _ *struct {
		ShareToken string `path:"share_token" doc:"The link's share token"`
		PodcastID  string `path:"podcast_id" doc:"The podcast's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r PodcastRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := func(reason string, cause error) {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID,
			"a shared podcast could not be served", reason, cause)
	}

	// The router first: a path it would not have matched never reaches the
	// Python route, so nothing below may answer it.
	if starlettepath.Unroutable(ctx.Param("share_token"), ctx.Param("podcast_id")) {
		starlettepath.WriteRouterNotFound(response, requestID)
		return
	}

	// Then the 422, before any row is read: FastAPI validates the path
	// before it calls the route.
	failures := make([]errorenvelope.Failure, 0, 1)
	podcastID := readPathInteger(ctx.Param("podcast_id"), &failures, "podcast_id")
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	found, err := liveSnapshot(ctx.Context(), r.Rows,
		starlettepath.AsDecodedByTheServer(ctx.Param("share_token")), clock(r.Now).now())
	if err != nil {
		failed("could not read the snapshot the token names", err)
		return
	}
	if found == nil {
		r.Errors.Write(response, http.StatusNotFound, podcastAbsent, requestID)
		return
	}
	data, err := snapshotData(found)
	if err != nil {
		failed("the snapshot's data could not be read as the Python route reads it", err)
		return
	}
	podcast, err := FindPodcast(data, podcastID.value)
	if err != nil {
		failed("the snapshot's podcasts could not be read as the Python route reads them", err)
		return
	}
	// `if not podcast_info`: an entry that matched has an `original_id`, so
	// it is never the empty dict this would also catch.
	if podcast == nil {
		r.Errors.Write(response, http.StatusNotFound, podcastAbsent, requestID)
		return
	}

	body, err := renderUnmodelled(BuildPodcast(podcast))
	if err != nil {
		failed("could not render the podcast", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
