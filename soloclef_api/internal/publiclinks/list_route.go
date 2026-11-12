package publiclinks

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Lister reads an account's links.
type Lister interface {
	Links(ctx context.Context, userID string) ([]Link, error)
}

// ListRoute serves GET /api/v1/snapshots: every public link the caller has
// made, newest first.
type ListRoute struct {
	Links Lister
	// FrontendURL is where the web app is served, which every link's
	// public_url is built from. A trailing slash is ignored.
	FrontendURL string
	Errors      errorenvelope.Writer
	Logger      *slog.Logger
}

func (r ListRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-public-links",
		Method:      http.MethodGet,
		Path:        ListPath,
		Summary:     "The caller's public share links",
		Tags:        []string{"public links"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ListRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := func(reason string, cause error) {
		serverError(ctx, r.Errors, r.Logger, response, requestID,
			"the public links could not be read", reason, cause)
	}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an anonymous list
		// if the wiring is ever wrong.
		failed(noCaller, nil)
		return
	}
	links, err := r.Links.Links(ctx.Context(), caller.UserID)
	if err != nil {
		failed("could not read the caller's public links", err)
		return
	}
	// The clock is read once, so every link in one answer is judged against
	// the same instant.
	body, err := pythonjson.Marshal(BuildList(links, strings.TrimRight(r.FrontendURL, "/"), time.Now().UTC()))
	if err != nil {
		failed("could not render the public link list", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

// noCaller is the reason logged when a route runs with nobody signed in.
const noCaller = "the route ran without an authenticated caller"

// serverError logs what went wrong and answers the tier's sanitised 500. The
// cause is for the log only. The CORS headers are left on the answer, so the
// web app can read the envelope and show its request id.
func serverError(
	ctx huma.Context,
	errors errorenvelope.Writer,
	logger *slog.Logger,
	response http.ResponseWriter,
	requestID, headline, reason string,
	cause error,
) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	logger.LogAttrs(ctx.Context(), slog.LevelError, headline, attributes...)
	errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
