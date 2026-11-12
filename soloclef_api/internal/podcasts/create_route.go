package podcasts

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// CreateRoute serves POST /api/v1/podcasts: a new podcast, waiting at the
// brief gate with a brief proposed for it (proposal.go).
//
// Nothing is enqueued and no model is asked: a podcast costs nothing until
// its brief is approved. That is also why the whole route is one
// transaction with no undo -- there is nothing outside the database to
// undo.
//
// The body is read by the tier's body reader before the route runs, so the
// pair is listed in cmd/api's `withBodies`.
type CreateRoute struct {
	Podcasts Database
	Voices   Catalogue
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

func (r CreateRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-podcast",
		Method:        http.MethodPost,
		Path:          CreatePath,
		Summary:       "Create a podcast and propose its brief",
		Tags:          []string{"podcasts"},
		DefaultStatus: http.StatusCreated,
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r CreateRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	answer := answerer{
		ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestcontext.ID(ctx.Context()),
		what:      "the podcast could not be created",
	}
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return
	}
	body, failures, err := decodeBody(ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if err != nil {
		answer.fail("a body reached the route that the body reader should have turned away", err)
		return
	}
	var request creation
	if failures == nil {
		request, failures = readCreation(body)
	}
	if len(failures) > 0 {
		answer.errors.WriteValidation(response, answer.requestID, failures)
		return
	}

	// Asked before the transaction, because it can mean a request to the
	// model proxy and a transaction must not be held open across one.
	roster, err := r.Voices.Roster(ctx.Context())
	if err != nil {
		answer.refuse(err, "could not read the voice catalogue")
		return
	}

	var created *Stored
	err = r.Podcasts.Write(ctx.Context(), func(ctx context.Context, rows Writing) error {
		// The thread a podcast is attached to is where its player appears,
		// so it has to be one of the caller's own. Another account's thread
		// is told exactly what a thread that was never made is told.
		if thread := request.ThreadID; thread != nil {
			owned, err := rows.OwnsThread(ctx, caller.UserID, *thread)
			if err != nil {
				return err
			}
			if !owned {
				return &refusal{status: http.StatusNotFound, message: threadAbsent}
			}
		}

		column, err := rows.LatestBrief(ctx, caller.UserID)
		if err != nil {
			return err
		}
		last, readable := choicesFrom(column)
		if !readable {
			r.Logger.LogAttrs(ctx, slog.LevelWarn,
				"the caller's last brief cannot be read; a new podcast starts from the defaults",
				slog.String("user_id", caller.UserID))
		}
		brief, err := propose(roster, last, request)
		if err != nil {
			return err
		}
		spec, err := pythonjson.Marshal(brief)
		if err != nil {
			return err
		}
		created, err = rows.Create(ctx, caller.UserID, NewPodcast{
			Title: request.Title, SourceContent: request.Source, Spec: spec,
			ThreadID: request.ThreadID,
		})
		return err
	})
	if err != nil {
		answer.refuse(err, "could not create the podcast")
		return
	}

	rendered, err := detail(ctx.Context(), r.Logger, created)
	if err != nil {
		answer.fail("the podcast is created and its answer could not be rendered", err)
		return
	}
	pythonjson.Send(response, http.StatusCreated, rendered)
}
