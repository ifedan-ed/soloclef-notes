// Package health answers whether this replica can serve.
//
// The answer is only about this replica: the proxy uses it to decide whether to
// send it traffic, so it checks the one dependency a request needs — the
// database — and nothing else. The path is this tier's own; the Python tier's
// probes stay the Python tier's.
package health

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Path is where the probe answers.
const Path = "/healthz"

// Database is the dependency a request needs.
type Database interface {
	Ping(ctx context.Context) error
}

// Route serves the probe.
type Route struct {
	Database Database
	Logger   *slog.Logger
}

func (r Route) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        Path,
		Summary:     "Whether this replica can serve requests",
		Tags:        []string{"operations"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r Route) serve(ctx huma.Context) {
	status := http.StatusOK
	database := "ok"
	if err := r.Database.Ping(ctx.Context()); err != nil {
		status = http.StatusServiceUnavailable
		database = "unreachable"
		r.Logger.ErrorContext(ctx.Context(), "the database did not answer the health probe",
			slog.String("request_id", requestcontext.ID(ctx.Context())),
			slog.String("error", err.Error()))
	}

	body := pythonjson.NewObject()
	body.Set("database", database)
	encoded, err := pythonjson.Marshal(body)
	if err != nil {
		encoded = []byte(`{"database":"unknown"}`)
		status = http.StatusServiceUnavailable
	}

	pythonjson.Send(humawriter.New(ctx), status, encoded)
}
