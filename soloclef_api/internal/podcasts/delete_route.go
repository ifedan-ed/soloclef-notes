package podcasts

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// DeleteRoute serves DELETE /api/v1/podcasts/{podcast_id}.
//
// It deletes the `podcast_runs` row and nothing else. The delivered episode
// -- the artifact, its document and its audio -- is a thing of its own in the
// Studio and is deleted there; a notebook output that named the podcast is
// left with a NULL by its foreign key.
//
// A podcast can be deleted in any state. One that is being drafted or
// rendered is deleted under its task, which finds no row when it next looks
// and stops (app/podcasts/tasks). Nothing else is owed: no balance is held
// against a podcast any more, so there is nothing to release.
type DeleteRoute struct {
	Podcasts Database
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

// Register adds the operation; the path parameter is a string for the reason
// DetailRoute.Register gives.
func (r DeleteRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "delete-podcast",
		Method:      http.MethodDelete,
		Path:        DeletePath,
		Summary:     "Delete a podcast",
		Tags:        []string{"podcasts"},
	}, func(ctx context.Context, _ *struct {
		PodcastID string `path:"podcast_id" doc:"The podcast's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r DeleteRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	answer := answerer{
		ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestcontext.ID(ctx.Context()),
		what:      "the podcast could not be deleted",
	}
	caller, id, proceed := answer.start()
	if !proceed {
		return
	}

	err := r.Podcasts.Write(ctx.Context(), func(ctx context.Context, rows Writing) error {
		podcast, err := load(ctx, rows, caller, id)
		if err != nil {
			return err
		}
		// Gone already, by another request between the read and here, is
		// what the caller asked for: the answer is the same.
		_, err = rows.Delete(ctx, caller.UserID, podcast.ID)
		return err
	})
	if err != nil {
		answer.refuse(err, "could not delete the podcast")
		return
	}

	// `{"message": …}` is what the web app's delete expects back.
	body := pythonjson.NewObject()
	body.Set("message", deleted)
	rendered, err := pythonjson.Marshal(body)
	if err != nil {
		answer.fail("the delete is committed and its answer could not be rendered", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, rendered)
}
