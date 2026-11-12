// Package agentactions serves the log of what the assistant changed in a
// thread:
//
//	GET /api/v1/threads/{thread_id}/actions?page=&page_size=   newest first
//
// The chat page asks for it whenever a thread is open, to decide whether a
// tool card or a turn has anything to undo.
//
// # The undo routes are internal/knowledgebase's
//
// `POST …/revert/{action_id}` and `POST …/revert-turn/{chat_turn_id}` live
// beside the tools whose changes they take back, because an undo is read
// from the revision those tools write in the same transaction as the change,
// and the two must agree on its format. This package only lists the log.
//
// # What it does better than the Python
//
//   - **The owner is named on the thread and on every row**, in the one
//     statement. The Python checked the thread's workspace and then listed
//     every row of the thread, whoever it was recorded for.
//   - **One statement** answers the page, each action's "undone by" and the
//     total; there were three.
//   - **It answers from the rows instead of a 503.** With the log off the
//     Python refused the route, and the chat page, which asks on every
//     thread, logged an error each time.
package agentactions

import "net/http"

// ListPath is the route's path. `{thread_id}` is the name every other route
// under /api/v1/threads/ uses for that segment (internal/chat/threads), which
// a ServeMux requires of patterns that share it.
const ListPath = "/api/v1/threads/{thread_id}/actions"

// Served is the route as a "METHOD /path" pair.
func Served() []string { return []string{http.MethodGet + " " + ListPath} }

const (
	threadAbsent    = "Thread not found"
	defaultPageSize = 50
	maxPageSize     = 200
	// maxPage keeps page × page_size inside the int32 the statement takes.
	maxPage = 1_000_000
)
