package installedbuilds

import (
	"fmt"
	"strings"
)

// An Answer is server code kept only because an installed build needs it:
// a route the current apps no longer call, a response field they no longer
// read, a query value they no longer send. Every one is listed here, and
// every file that holds one carries the line
//
//	// installed-builds: <ID>
//
// beside it. compat_test.go holds the two together: each answer is needed
// by at least one snapshot (Needs), and each marker in the server's source
// names an answer listed here. So when a snapshot is removed, the test names
// every answer no build needs any more and every place to delete it from;
// nothing lingers, and nothing is kept without saying for whom.
type Answer struct {
	// ID is the marker's name.
	ID string
	// Needs is what a build must do for the answer to be needed; it is
	// needed while any snapshot does any one of them.
	Needs []Need
	// Why is what breaks on a phone without it.
	Why string
	// Code is the files that hold it, relative to soloclef_api.
	Code []string
}

// A Need is one thing an installed build does that an Answer serves. Set
// exactly one of Field and Query, or neither for the route itself.
type Need struct {
	// Operation is "METHOD /path", as calls.json names it.
	Operation string
	// Field is a response field the build requires, by its dotted path in
	// the snapshot's success answer ("announcements"; a list's items are
	// stepped into).
	Field string
	// Query is a query parameter and the value the build sends in it:
	// {"category", "announcements"}.
	Query [2]string
}

func (n Need) String() string {
	switch {
	case n.Field != "":
		return fmt.Sprintf("%s reading %q", n.Operation, n.Field)
	case n.Query[0] != "":
		return fmt.Sprintf("%s?%s=%s", n.Operation, n.Query[0], n.Query[1])
	}
	return n.Operation
}

// Answers is every compatibility answer the server holds today.
var Answers = []Answer{
	{
		ID: "notifications-announcements-tab",
		Needs: []Need{
			{Operation: "GET /api/v1/notifications", Query: [2]string{"category", "announcements"}},
			{Operation: "DELETE /api/v1/notifications", Query: [2]string{"category", "announcements"}},
		},
		Why: "1.1.0 lists and clears the retired announcements tab every time the Inbox opens (inboxModel.ts " +
			"INBOX_CATEGORIES) and shows an error on a 422. It is an empty view: nothing listed, nothing cleared.",
		Code: []string{"internal/notifications/notifications.go", "internal/notifications/view.go", "internal/notifications/routes.go"},
	},
	{
		ID:    "model-roles-image",
		Needs: []Need{{Operation: "GET /api/v1/users/me/model-roles", Field: "image"}},
		Why: "1.1.0 requires image in the model roles and show the Models screen's error without it; " +
			"nothing is drawn by a model since 2026-11-14, so it is always no model and an empty menu, " +
			"and a PUT that clears it changes nothing.",
		Code: []string{"internal/modelchoices/roles_route.go"},
	},
	{
		ID:    "infographic-retired-styles",
		Needs: []Need{{Operation: "POST /api/v1/notebooks/{notebook_id}/studio/generate"}},
		Why: "1.1.0 offers the image model's styles kawaii, clay, anime and bricks, which a drawn poster " +
			"has no counterpart for, and would show a refusal for a choice the app offered; they are made in auto.",
		Code: []string{"internal/studiogenerate/request.go"},
	},
	{
		ID:    "retired-operator-connections",
		Needs: []Need{{Operation: "GET /api/v1/global-model-connections"}},
		Why: "Admin, Model connections shows only \"Couldn't load\" when the operator's list fails, hiding the " +
			"account's own connections with it. Operator connections are not offered, so the list is empty.",
		Code: []string{"internal/retiredsettings/retiredsettings.go", "cmd/api/main.go"},
	},
	{
		ID:    "retired-notebook-links",
		Needs: []Need{{Operation: "GET /api/v1/notebook-links"}},
		Why: "Settings, Public links reads the notebook links beside the output links and shows \"Couldn't load\" " +
			"when either fails, so on a 404 nobody could see or revoke their output links. It is always empty.",
		Code: []string{"internal/retiredshare/retiredshare.go", "cmd/api/main.go"},
	},
	{
		ID:    "retired-notebook-share",
		Needs: []Need{{Operation: "POST /api/v1/notebooks/{notebook_id}/public-link"}},
		Why: "the notebook menu's Share says \"This can't be shared publicly.\" on a 403 and " +
			"\"Couldn't create the link. Please try again.\" on a 404, which invites trying for ever.",
		Code: []string{"internal/retiredshare/retiredshare.go", "cmd/api/main.go"},
	},
	{
		ID:    "retired-chat-share",
		Needs: []Need{{Operation: "POST /api/v1/threads/{thread_id}/snapshots"}},
		Why:   "sharing a chat is refused with the same 403, for the same reason as a notebook.",
		Code:  []string{"internal/retiredshare/retiredshare.go", "cmd/api/main.go"},
	},
}

// A Retirement is a call an installed build makes that the server, on
// purpose, no longer serves: the build gets Status (the router's 404, its 405
// for a method on a path that serves another, or the gate's 401 for a link
// page asked without a credential), and Why says what
// the build does with it and why that is right. It is the one other way a
// call may go unserved; anything not listed here or answered above is a
// broken phone (cmd/api's TestEveryRouteAnInstalledBuildCallsIsServed).
type Retirement struct {
	Operation string
	Status    int
	Why       string
}

// Retired is every call an installed build makes that is answered without
// a route by decision. Each is still made by a snapshot (compat_test.go),
// and its contract case expects Status.
var Retired = []Retirement{
	{
		Operation: "GET /api/v1/notebooks/{notebook_id}/public-link", Status: 405,
		Why: "no notebook link exists since migration 00019 (only a Studio output is shared); the notebook menu " +
			"reads a failed read as no live link (useNotebookLink, liveOf(undefined) is null), so nothing on screen breaks",
	},
	{
		Operation: "DELETE /api/v1/notebooks/{notebook_id}/public-link", Status: 405,
		Why: "Stop sharing is offered only while the menu shows a live notebook link, which it never can now",
	},
	{
		Operation: "DELETE /api/v1/notebook-links/{link_id}", Status: 404,
		Why: "a notebook link's revoke is offered only for a link the Public links page listed, and that list is always empty",
	},
	{
		Operation: "POST /api/v1/diagrams/mermaid", Status: 404,
		Why: "never served by this server (the app's planned route B5); the chat shows the diagram's code, as it does offline, " +
			"and says so without offering Try again",
	},
	gonePage("GET /api/v1/public-notebooks/{token}"),
	gonePage("GET /api/v1/public-notebooks/{token}/sources/{source_id}"),
	gonePage("GET /api/v1/public-notebooks/{token}/artifacts/{artifact_id}/manifest"),
	gonePage("GET /api/v1/public-notebooks/{token}/artifacts/{artifact_id}/download"),
	gonePage("GET /api/v1/public/{share_token}/podcasts/{podcast_id}"),
	gonePage("GET /api/v1/public/{share_token}/podcasts/{podcast_id}/stream"),
}

// gonePage is a public page of a shared notebook or chat, which no longer
// exist: only a Studio output is shared. Asked without a credential, a path
// no route serves meets the gate first, so the answer is its 401.
func gonePage(operation string) Retirement {
	return Retirement{Operation: operation, Status: 401,
		Why: "no notebook link or shared chat exists any more; the bundle reads the 401 as \"this link is gone\" " +
			"(features/public/publicApi.ts), which is true"}
}

// NeededBy says which builds make a retired call, by Label.
func (r Retirement) NeededBy(builds []Build) []string {
	return Answer{Needs: []Need{{Operation: r.Operation}}}.NeededBy(builds)
}

// IsRetired says whether an operation is a listed retirement.
func IsRetired(operation string) (Retirement, bool) {
	for _, retired := range Retired {
		if sameOperation(retired.Operation, operation) {
			return retired, true
		}
	}
	return Retirement{}, false
}

// NeededBy says which builds need an answer, by Label.
func (a Answer) NeededBy(builds []Build) []string {
	var out []string
	for _, build := range builds {
		for _, need := range a.Needs {
			if build.does(need) {
				out = append(out, build.Label())
				break
			}
		}
	}
	return out
}

func (b Build) does(need Need) bool {
	for _, call := range b.Calls {
		if !sameOperation(call.Operation(), need.Operation) {
			continue
		}
		switch {
		case need.Field != "":
			return b.RequiresField(call, need.Field)
		case need.Query[0] != "":
			values, sent := call.Query[need.Query[0]]
			if !sent {
				return false
			}
			for _, value := range values {
				if value == need.Query[1] {
					return true
				}
			}
			return false
		}
		return true
	}
	return false
}

// sameOperation compares two "METHOD /path" names with the parameters'
// names ignored: the app's snapshot and the server may call {token}
// {share_token}.
func sameOperation(a, b string) bool { return Shape(a) == Shape(b) }

// Shape is an operation with every path parameter written as {}.
func Shape(operation string) string {
	var out strings.Builder
	depth := 0
	for _, r := range operation {
		switch {
		case r == '{':
			depth++
			if depth == 1 {
				out.WriteString("{}")
			}
		case r == '}':
			depth--
		case depth == 0:
			out.WriteRune(r)
		}
	}
	return out.String()
}

// Marker is the comment that ties a file to an answer.
func Marker(id string) string { return "installed-builds: " + id }
