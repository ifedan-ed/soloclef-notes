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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/starlettepath"
)

// PageRoute serves what a public link shows: a chat's messages, or one
// Studio output and its files.
type PageRoute struct {
	Rows   Rows
	Errors errorenvelope.Writer
	Logger *slog.Logger
	// Now is the clock a link's expiry is measured against; nil is the
	// system's.
	Now func() time.Time
}

// Register adds the operation. The one path parameter is a plain string on
// both tiers, so there is nothing to coerce and no 422 this route can answer.
func (r PageRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-public-chat",
		Method:      http.MethodGet,
		Path:        PagePath,
		Summary:     "What a public share link shows",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/public/{share_token}, byte for byte. " +
			"Answers without a credential.",
		Tags: []string{"public"},
	}, func(ctx context.Context, _ *struct {
		ShareToken string `path:"share_token" doc:"The link's share token"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r PageRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := func(reason string, cause error) {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID,
			"a public link's page could not be served", reason, cause)
	}

	if starlettepath.Unroutable(ctx.Param("share_token")) {
		starlettepath.WriteRouterNotFound(response, requestID)
		return
	}
	token := starlettepath.AsDecodedByTheServer(ctx.Param("share_token"))

	// _snapshot_row and not get_snapshot_by_token: this route reads the row
	// itself so that it can tell an expired link from one that was never
	// there. The other two routes of this package answer both the same way.
	found, err := r.Rows.SnapshotByToken(ctx.Context(), token)
	if err != nil {
		failed("could not read the snapshot the token names", err)
		return
	}
	if found == nil {
		r.Errors.Write(response, http.StatusNotFound, pageAbsent, requestID)
		return
	}
	if expired(found.ExpiresAt, clock(r.Now).now()) {
		r.Errors.Write(response, http.StatusGone, pageExpired, requestID)
		return
	}

	data, err := snapshotData(found)
	if err != nil {
		failed("the snapshot's data could not be read as the Python route reads it", err)
		return
	}
	page, err := BuildPage(ctx.Context(), r.Rows, token, *found, data)
	if err != nil {
		failed("the page could not be built as the Python route builds it", err)
		return
	}
	body, err := pythonjson.Marshal(page)
	if err != nil {
		failed("could not render the page", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
