package documents

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
)

// TypeCount is how many documents there are of one type.
type TypeCount struct {
	DocumentType string
	Count        int64
}

// TypeCounts reads what the counts by type read, in the order of the type
// itself -- the order the enum's labels were declared in, not the
// alphabet's.
type TypeCounts interface {
	TypeCountsOf(ctx context.Context, userID string) ([]TypeCount, error)
}

// TypeCountsRoute serves GET /documents/type-counts
// (get_document_type_counts): how many documents there are of each type, as
// one JSON object from type to count.
//
// The counts are the caller's own documents. The keys are in the order of
// the rows, and the statement ends `ORDER BY documents.document_type`, so
// there is one sequence of bytes to answer with.
//
// A 500 carries the CORS headers (failure.go).
type TypeCountsRoute struct {
	Counts TypeCounts
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r TypeCountsRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "count-documents-by-type",
		Method:      http.MethodGet,
		Path:        TypeCountsPath,
		Summary:     "How many documents there are of each type",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/documents/type-counts, byte for byte.",
		Tags: []string{"documents"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r TypeCountsRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, what: "the counts of documents by type could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.uncaught(noCaller, nil)
		return
	}

	counts, err := r.Counts.TypeCountsOf(ctx.Context(), caller.UserID)
	if err != nil {
		failed.caught("could not count the caller's documents by type", err)
		return
	}

	// One key a type, in the order of the rows.
	body := pythonjson.NewObject()
	for _, count := range counts {
		body.Set(count.DocumentType, count.Count)
	}
	encoded, err := pythonjson.Marshal(body)
	if err != nil {
		failed.uncaught(answerUnrendered, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, encoded)
}
