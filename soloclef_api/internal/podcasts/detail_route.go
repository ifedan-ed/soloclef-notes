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

// DetailRoute serves GET /api/v1/podcasts/{podcast_id}: one podcast, with its
// brief and its transcript.
type DetailRoute struct {
	Podcasts Database
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

// Register adds the operation. The path parameter is a string, here and in
// every route of this package, so that a value that is not a number is
// answered with this tier's own envelope (internal/requestnumber) rather
// than with Huma's.
func (r DetailRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-podcast",
		Method:      http.MethodGet,
		Path:        DetailPath,
		Summary:     "One podcast, with its brief and transcript",
		Tags:        []string{"podcasts"},
	}, func(ctx context.Context, _ *struct {
		PodcastID string `path:"podcast_id" doc:"The podcast's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r DetailRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	answer := answerer{
		ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestcontext.ID(ctx.Context()),
		what:      "the podcast could not be read",
	}
	caller, id, proceed := answer.start()
	if !proceed {
		return
	}

	var podcast *Stored
	err := r.Podcasts.Read(ctx.Context(), func(ctx context.Context, rows Reading) error {
		loaded, err := load(ctx, rows, caller, id)
		podcast = loaded
		return err
	})
	if err != nil {
		answer.refuse(err, "could not read the podcast")
		return
	}

	body, err := detail(ctx.Context(), r.Logger, podcast)
	if err != nil {
		answer.fail("the podcast could not be rendered", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
