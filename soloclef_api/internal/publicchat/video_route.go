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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/starlettepath"
)

// VideoRoute serves the payload a shared video artifact is played from: its
// slides and the scene code for each.
//
// Unlike the podcast, the payload is **not** in the snapshot. The snapshot
// holds only the artifact's id, in its `artifact_ids` allowlist, and the
// slides and scene codes are read off the artifact as it is now. So a video
// regenerated after it was shared is served as regenerated to every link
// that already existed (disclosure_test.go).
type VideoRoute struct {
	Rows   Rows
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is the clock a link's expiry is measured against; nil is the
	// system's.
	Now func() time.Time
}

// Register adds the operation; the path parameters are strings for the
// reason PodcastRoute.Register gives.
func (r VideoRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-public-artifact-video",
		Method:      http.MethodGet,
		Path:        VideoPath,
		Summary:     "The slides and scene codes of a video shared by a public link",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/public/{share_token}/artifacts/{artifact_id}/video, " +
			"byte for byte. Answers without a credential.",
		Tags: []string{"public"},
	}, func(ctx context.Context, _ *struct {
		ShareToken string `path:"share_token" doc:"The link's share token"`
		ArtifactID string `path:"artifact_id" doc:"The artifact's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r VideoRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := func(reason string, cause error) {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID,
			"a shared video's payload could not be served", reason, cause)
	}

	if starlettepath.Unroutable(ctx.Param("share_token"), ctx.Param("artifact_id")) {
		starlettepath.WriteRouterNotFound(response, requestID)
		return
	}
	failures := make([]errorenvelope.Failure, 0, 1)
	artifactID := readPathInteger(ctx.Param("artifact_id"), &failures, "artifact_id")
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	token := starlettepath.AsDecodedByTheServer(ctx.Param("share_token"))
	found, err := liveSnapshot(ctx.Context(), r.Rows, token, clock(r.Now).now())
	if err != nil {
		failed("could not read the snapshot the token names", err)
		return
	}
	if found == nil {
		r.Errors.Write(response, http.StatusNotFound, videoAbsent, requestID)
		return
	}
	data, err := snapshotData(found)
	if err != nil {
		failed("the snapshot's data could not be read as the Python route reads it", err)
		return
	}

	// The allowlist, before the artifact is looked for at all: a link reaches
	// the artifacts its own thread produced and no other, and an id it does
	// not share is the same 404 as one that does not exist -- with no
	// statement issued, so the answer does not even take longer.
	shared, err := containsInt(data.GetOrNil("artifact_ids"), artifactID.value)
	if err != nil {
		failed("the snapshot's artifact_ids could not be read as the Python route reads them", err)
		return
	}
	if !shared {
		r.Errors.Write(response, http.StatusNotFound, videoAbsent, requestID)
		return
	}

	// Only now does the id's width matter. Python holds an integer of any
	// size and it is this statement that fails on one no column can hold --
	// after the allowlist, so a wide id the link does not share is the 404
	// above and not a 500.
	id, fits := artifactID.narrow()
	if !fits {
		failed("the artifact id is wider than any statement can carry", requestnumber.ErrOutOfRange)
		return
	}
	artifact, err := r.Rows.VideoArtifact(ctx.Context(), id)
	if err != nil {
		failed("could not read the artifact", err)
		return
	}
	if artifact == nil {
		r.Errors.Write(response, http.StatusNotFound, videoAbsent, requestID)
		return
	}

	payload, absent, err := BuildVideo(token, *artifact)
	if err != nil {
		failed("the artifact's metadata could not be read as the Python route reads it", err)
		return
	}
	if absent {
		r.Errors.Write(response, http.StatusNotFound, payloadAbsent, requestID)
		return
	}
	body, err := renderUnmodelled(payload)
	if err != nil {
		failed("could not render the video payload", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
