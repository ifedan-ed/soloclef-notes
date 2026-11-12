package academicsearch

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/academic"
)

// Sources is the three searches (*academic.Sources, the chat's own).
type Sources interface {
	PubMed(ctx context.Context, query string, limit int, fullText bool, sinceYear, untilYear int) ([]academic.Paper, error)
	Arxiv(ctx context.Context, query string, limit int) ([]academic.Paper, error)
	Preprints(ctx context.Context, query, server string, limit int) ([]academic.Paper, error)
}

// Routes is the three routes.
type Routes struct {
	Sources Sources
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	r.mount(api, PubMedPath, "pubmed", "Search PubMed", func(ctx context.Context, s search) ([]academic.Paper, error) {
		return r.Sources.PubMed(ctx, s.Query, s.Results, s.FullText, s.Since, s.Until)
	})
	r.mount(api, ArxivPath, "arxiv", "Search arXiv", func(ctx context.Context, s search) ([]academic.Paper, error) {
		return r.Sources.Arxiv(ctx, s.Query, s.Results)
	})
	r.mount(api, PreprintPath, "medrxiv", "Search medRxiv and bioRxiv", func(ctx context.Context, s search) ([]academic.Paper, error) {
		return r.Sources.Preprints(ctx, s.Query, s.Server, s.Results)
	})
}

func (r Routes) mount(api huma.API, path, source, summary string, run func(context.Context, search) ([]academic.Paper, error)) {
	huma.Register(api, huma.Operation{
		OperationID: "academic-search-" + source, Method: http.MethodPost, Path: path, Summary: summary, Tags: []string{"academic-search"},
	}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: func(ctx huma.Context) {
			response, requestID := humawriter.New(ctx), requestcontext.ID(ctx.Context())
			// The searches read public data, but they are this account's
			// requests to make: the gate has to have named a caller.
			if _, ok := principal.FromContext(ctx.Context()); !ok {
				r.Logger.ErrorContext(ctx.Context(), "an academic search ran without a caller", slog.String("request_id", requestID))
				r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
				return
			}
			raw, err := io.ReadAll(io.LimitReader(ctx.BodyReader(), maxBodyBytes+1))
			if err != nil || len(raw) > maxBodyBytes {
				r.Errors.Write(response, http.StatusRequestEntityTooLarge, "The search request is too large.", requestID)
				return
			}
			asked, failures := readSearch(raw, path)
			if len(failures) > 0 {
				r.Errors.WriteValidation(response, requestID, failures)
				return
			}
			searching, done := context.WithTimeout(ctx.Context(), searchTimeout)
			defer done()
			papers, err := run(searching, asked)
			switch {
			case err == nil:
			case errors.Is(err, context.Canceled):
				// The caller went away; nobody reads an answer.
				return
			case errors.Is(err, context.DeadlineExceeded):
				r.Errors.Write(response, http.StatusGatewayTimeout, "The source took too long to answer. Please try again.", requestID)
				return
			default:
				// The sources' errors are sentences that name the host and
				// nothing of this deployment ("export.arxiv.org answered
				// 503"), so the caller is told which source failed.
				r.Logger.WarnContext(ctx.Context(), "an academic source failed",
					slog.String("request_id", requestID), slog.String("source", source), slog.String("error", err.Error()))
				r.Errors.Write(response, http.StatusBadGateway, "The search could not be run: "+err.Error()+".", requestID)
				return
			}
			if len(papers) > asked.Results {
				papers = papers[:asked.Results]
			}
			response.Header().Set("Content-Type", "application/json")
			response.Header().Set("Cache-Control", "private, no-store")
			response.WriteHeader(http.StatusOK)
			encoder := json.NewEncoder(response)
			// Titles carry "<" and "&"; they are JSON, not HTML.
			encoder.SetEscapeHTML(false)
			_ = encoder.Encode(rowsOf(source, papers))
		}}, nil
	})
}
