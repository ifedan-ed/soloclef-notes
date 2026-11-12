package artifacts

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// Videos reads one artifact for the video route.
type Videos interface {
	VideoIn(ctx context.Context, userID string, artifactID int64) (*Video, error)
}

// VideoRoute serves the payload a video artifact is played from: its slides
// and the scene code for each.
type VideoRoute struct {
	Videos Videos
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Register adds the operation; the path parameters are strings for the reason
// ListRoute.Register gives.
func (r VideoRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-artifact-video",
		Method:      http.MethodGet,
		Path:        VideoPath,
		Summary:     "The slides and scene codes of a video artifact",
		Tags:        []string{"artifacts"},
	}, func(ctx context.Context, _ *struct {
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
			"the video payload could not be served", reason, cause)
	}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed(noCaller, nil)
		return
	}

	// The 422 first, before anything is read.
	failures := make([]errorenvelope.Failure, 0, 1)
	artifact := readNumber(ctx.Param("artifact_id"), &failures, "path", "artifact_id")
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	if artifact.tooWide {
		failed(artifactTooWide, requestnumber.ErrOutOfRange)
		return
	}
	found, err := r.Videos.VideoIn(ctx.Context(), caller.UserID, artifact.value)
	if err != nil {
		failed("could not read the artifact", err)
		return
	}
	if found == nil {
		// Unknown, or another account's: the same answer, so the route does
		// not say which ids are in use.
		r.Errors.Write(response, http.StatusNotFound, artifactAbsent, requestID)
		return
	}

	payload, refusal := BuildVideo(*found)
	if refusal != nil {
		r.Errors.Write(response, refusal.Status, refusal.Message, requestID)
		return
	}
	body, err := pythonjson.Marshal(payload)
	if err != nil {
		failed("could not render the video payload", err)
		return
	}
	// No Cache-Control here: unlike the list, this route sets none.
	pythonjson.Send(response, http.StatusOK, body)
}
