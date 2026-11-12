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

// Lister reads the list.
type Lister interface {
	ArtifactsIn(ctx context.Context, userID string, threadID *int64) ([]Listed, error)
}

// ListRoute serves the caller's artifacts.
type ListRoute struct {
	Artifacts Lister
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
}

// Register adds the operation. `thread_id` is not declared to Huma, so that
// a value that is not a whole number is refused with this tier's own 422
// envelope (internal/requestnumber) rather than with Huma's; it is read off
// the query string.
func (r ListRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-artifacts",
		Method:      http.MethodGet,
		Path:        ListPath,
		Summary:     "Every artifact the caller owns",
		Tags:        []string{"artifacts"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ListRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := func(reason string, cause error) {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID,
			"the artifact list could not be served", reason, cause)
	}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// read if the wiring is ever wrong.
		failed(noCaller, nil)
		return
	}

	// The 422 first, before anything is read.
	failures := make([]errorenvelope.Failure, 0, 1)
	// `thread_id: int | None = None`. Absent is None and the list is not
	// filtered. Present is read, and an empty value is present: `?thread_id=`
	// is a 422, not an absence. A parameter named twice is its last value
	// (internal/requestnumber.QueryValue), so `?thread_id=abc&thread_id=2` is
	// thread 2 and the other way round is a 422.
	var thread *number
	requested := ctx.URL()
	if text, named := requestnumber.QueryValue(requested.Query(), "thread_id"); named {
		read := readNumber(text, &failures, "query", "thread_id")
		thread = &read
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	// The list is the caller's own, filtered by owner in the statement, so
	// a thread id that is another account's simply matches nothing: the
	// thread itself is never read and nothing says whether it exists.
	var threadID *int64
	if thread != nil {
		if thread.tooWide {
			failed(threadTooWide, requestnumber.ErrOutOfRange)
			return
		}
		threadID = &thread.value
	}
	rows, err := r.Artifacts.ArtifactsIn(ctx.Context(), caller.UserID, threadID)
	if err != nil {
		failed("could not read the caller's artifacts", err)
		return
	}

	body, err := pythonjson.Marshal(BuildList(rows))
	if err != nil {
		failed("could not render the artifact list", err)
		return
	}
	// `response.headers["Cache-Control"]`, which only a response the route
	// returned carries: no refusal above has it.
	response.Header().Set("Cache-Control", privateNoStore)
	pythonjson.Send(response, http.StatusOK, body)
}
