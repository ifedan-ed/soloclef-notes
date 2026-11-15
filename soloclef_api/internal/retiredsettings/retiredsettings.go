// Package retiredsettings answers five routes of Settings and Admin pages
// whose features were removed (on 2026-11-13, and agent sign-in on
// 2026-11-15), for installed phone builds.
//
//	GET  /api/v1/global-model-connections  200 []
//	GET  /api/v1/prompts/public            200 []
//	GET  /api/v1/admin/announcements       200 {"announcements": []}
//	POST /api/v1/admin/announcements       403, the error envelope
//	GET  /api/v1/agent-sign-in             200 {"enabled": false, ...}
//
// # Why they are here
//
// The phone bundles of 1.0.0 and 1.1.0 (internal/installedbuilds) open
// these pages, and each shows only "Couldn't load" when its list is a 404:
// Model connections then hides the account's own connections with the
// operator's, so nobody could see or switch off their own keys; Community
// prompts and Announcements show nothing but the error. An empty list is
// the truth now (operator connections are not offered, nothing is shared,
// announcements are not sent), and the pages work again around it. Sending
// an announcement is refused in the error envelope, which the page shows
// as its failure. The updates after these bundles remove the pages.
//
// Connect your agent asks for the agent sign-in details and shows
// "Couldn't load the sign-in details." on a 404. Agents connect with an API
// key only, so the answer is sign-in off, which is what the server always
// answered with no agent provider configured: the screen then shows the API
// key steps alone, as it did.
//
// Nothing is read or written: the answer is the same for every caller, so
// it says nothing about anyone. The routes stand behind the gate like every
// route of an account.
//
// They go when no installed bundle calls them (compat.go of
// internal/installedbuilds lists each with the bundles that need it).
// installed-builds: retired-operator-connections
// installed-builds: retired-community-prompts
// installed-builds: retired-announcements
// installed-builds: retired-agent-sign-in
package retiredsettings

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
	OperatorConnectionsPath = "/api/v1/global-model-connections"
	CommunityPromptsPath    = "/api/v1/prompts/public"
	AnnouncementsPath       = "/api/v1/admin/announcements"
	AgentSignInPath         = "/api/v1/agent-sign-in"
)

// NotSent is the refusal of a new announcement.
const NotSent = "Announcements were removed: nothing is sent."

// Served is the routes as "METHOD /path" pairs, for the gate's list.
func Served() []string {
	return []string{
		http.MethodGet + " " + OperatorConnectionsPath,
		http.MethodGet + " " + CommunityPromptsPath,
		http.MethodGet + " " + AnnouncementsPath,
		http.MethodPost + " " + AnnouncementsPath,
		http.MethodGet + " " + AgentSignInPath,
	}
}

// Routes is the five routes.
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
		{http.MethodGet, OperatorConnectionsPath, "retired-operator-connections", "Operator model connections (none: for installed phone builds)", empty(`[]`)},
		{http.MethodGet, CommunityPromptsPath, "retired-community-prompts", "Community prompts (none: for installed phone builds)", empty(`[]`)},
		{http.MethodGet, AnnouncementsPath, "retired-announcements", "Announcements (none: for installed phone builds)", empty(`{"announcements":[]}`)},
		{http.MethodPost, AnnouncementsPath, "retired-announcement-send", "Send an announcement (refused: for installed phone builds)", r.refuse},
		// The bundles read this shape (agentSignInSchema): both values null
		// when there is nothing to paste.
		{http.MethodGet, AgentSignInPath, "retired-agent-sign-in", "Agent sign-in details (off: for installed phone builds)", empty(`{"enabled":false,"client_id":null,"client_secret":null}`)},
	} {
		answer := route.answer
		huma.Register(api, huma.Operation{
			OperationID: route.id, Method: route.method, Path: route.path,
			Summary: route.summary, Tags: []string{"installed builds"},
		}, func(_ context.Context, _ *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: func(ctx huma.Context) {
				response, requestID := humawriter.New(ctx), requestcontext.ID(ctx.Context())
				// The gate names a caller before any of these runs; one
				// without is a wiring mistake, not a stranger.
				if _, ok := principal.FromContext(ctx.Context()); !ok {
					r.Logger.ErrorContext(ctx.Context(), "a retired settings route ran without a caller", slog.String("request_id", requestID))
					r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
					return
				}
				answer(response, requestID)
			}}, nil
		})
	}
}

// empty answers a list that has nothing in it any more, in the shape the
// installed builds parse.
func empty(body string) func(http.ResponseWriter, string) {
	encoded := json.RawMessage(body)
	return func(response http.ResponseWriter, _ string) {
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "private, no-store")
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write(encoded)
	}
}

// refuse answers a send the server no longer makes.
func (r Routes) refuse(response http.ResponseWriter, requestID string) {
	r.Errors.Write(response, http.StatusForbidden, NotSent, requestID)
}
