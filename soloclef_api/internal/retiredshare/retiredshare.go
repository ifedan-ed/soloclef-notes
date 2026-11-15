// Package retiredshare answers three sharing routes that were removed when
// public links became Studio outputs only, for installed phone builds.
//
//	GET  /api/v1/notebook-links                       200 {"links": []}
//	POST /api/v1/notebooks/{notebook_id}/public-link  403, the error envelope
//	POST /api/v1/threads/{thread_id}/snapshots        403, the error envelope
//
// # Why they are here
//
// The App Store builds 1.0.0 and 1.1.0 still call them. Settings → Public
// links reads the notebook links beside the output links (GET
// /api/v1/snapshots, internal/publiclinks) and shows "Couldn't load" when
// either fails, so on a 404 nobody could see or revoke their output links
// from those builds. Sharing a notebook or a chat from the notebook menu
// says "This can't be shared publicly." on a 403, which is the truth now;
// on a 404 it says "Couldn't create the link. Please try again.", which
// invites trying again for ever.
//
// So the list is empty (no notebook link exists any more: migration 00019
// removed them) and the two creates are refused. Nothing is read or
// written: the answer is the same for every notebook and chat, the caller's
// or not, so it says nothing about one. The routes stand behind the gate
// like every route of an account (cmd/api lists them as gated), so a
// stranger gets the gate's 401 first.
//
// They go when no installed build calls them. No other retired route is
// answered: a build that is not in the App Store is not kept working.
package retiredshare

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// The paths, as the installed phone builds call them.
const (
	NotebookLinksPath = "/api/v1/notebook-links"
	NotebookLinkPath  = "/api/v1/notebooks/{notebook_id}/public-link"
	ChatSnapshotPath  = "/api/v1/threads/{thread_id}/snapshots"
)

// NotShareable is the refusal of the two creates. The phone shows its own
// sentence for a 403; the web never calls these routes.
const NotShareable = "Only a Studio output can be shared publicly."

// Served is the routes as "METHOD /path" pairs, for the gate's list.
func Served() []string {
	return []string{
		http.MethodGet + " " + NotebookLinksPath,
		http.MethodPost + " " + NotebookLinkPath,
		http.MethodPost + " " + ChatSnapshotPath,
	}
}

// Routes is the three routes.
type Routes struct {
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Register mounts them.
func (r Routes) Register(api huma.API) {
	for _, route := range []struct {
		method, path, id, summary string
		answer                    func(http.ResponseWriter, string)
	}{
		// installed-builds: retired-notebook-links
		// installed-builds: retired-notebook-share
		// installed-builds: retired-chat-share
		{http.MethodGet, NotebookLinksPath, "retired-notebook-links", "Notebook links (none: for installed phone builds)", r.noLinks},
		{http.MethodPost, NotebookLinkPath, "retired-notebook-public-link", "Share a notebook (refused: for installed phone builds)", r.refuse},
		{http.MethodPost, ChatSnapshotPath, "retired-chat-snapshot", "Share a chat (refused: for installed phone builds)", r.refuse},
	} {
		answer := route.answer
		huma.Register(api, huma.Operation{
			OperationID: route.id, Method: route.method, Path: route.path,
			Summary: route.summary, Tags: []string{"sharing"},
		}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: func(ctx huma.Context) {
				response, requestID := humawriter.New(ctx), requestcontext.ID(ctx.Context())
				// The gate names a caller before any of these runs; one
				// without is a wiring mistake, not a stranger.
				if _, ok := principal.FromContext(ctx.Context()); !ok {
					r.Logger.ErrorContext(ctx.Context(), "a retired sharing route ran without a caller", slog.String("request_id", requestID))
					r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
					return
				}
				answer(response, requestID)
			}}, nil
		})
	}
}

// noLinks is the notebook-link list the installed builds read: always
// empty, in the shape they parse ({"links": [...]}).
func (Routes) noLinks(response http.ResponseWriter, _ string) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "private, no-store")
	response.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(response).Encode(struct {
		Links []struct{} `json:"links"`
	}{Links: []struct{}{}})
}

// refuse answers a create the server no longer makes.
func (r Routes) refuse(response http.ResponseWriter, requestID string) {
	r.Errors.Write(response, http.StatusForbidden, NotShareable, requestID)
}
