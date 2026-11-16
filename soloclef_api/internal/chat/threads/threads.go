// Package threads serves a chat thread's own routes: making one, listing
// and searching an account's, loading one's messages, renaming, archiving,
// deleting, and the list of tools the composer's tool picker shows.
//
// A thread is its owner's and nobody else's. There is no sharing a thread,
// with other accounts or by public link: only a Studio output can be shared
// (internal/publicshare), by the owner's decision of 2026-11-13.
//
// The turn routes (ask, resume, regenerate, stop) are package chat's. This
// package never runs a model; it is the rows around a conversation.
//
// # The contracts kept
//
// The shapes the web and phone apps read are in render.go, which names the
// reader of each. A message record is as it has always been.
// A thread record is what is left once nothing is shared: its title, its
// archive flag, its id and its two times, with no visibility and no
// creator, because a thread is only ever answered to its owner.
//
// # What this does better than the routes it replaces
//
//   - Deleting a thread removes the engine's saved conversation with it
//     (Service.Forget), and cannot race a running turn: the delete stops
//     the turn and holds the thread's own lease while it works (delete.go).
//     The old delete left the saved conversation behind for good.
//   - Messages come back in the order (created_at, id). A turn writes its
//     question and its answer row in one transaction, so the two share a
//     timestamp, and ordered by the timestamp alone the answer could load
//     above its question.
//   - One access rule, in one place (gate.go) and again in every statement
//     (queries.sql): the thread is the caller's, or it is "Thread not
//     found", the same as for an id that does not exist.
//   - The list's limit and the search are done by the database. Every
//     thread used to be read and the list cut in memory, and a search
//     for "%" matched every thread.
//   - Renaming or archiving a thread does not move it to the top of the
//     list: updated_at says when a thread was last talked in.
//   - A message appended by a client cannot be a "system" message and
//     cannot bring its own token usage. Usage is written by the turn that
//     spent it; a client's word for what it cost used to be stored.
package threads

import "strconv"

// The paths, as the apps call them.
const (
	ThreadsPath  = "/api/v1/threads"
	SearchPath   = "/api/v1/threads/search"
	ThreadPath   = "/api/v1/threads/{thread_id}"
	FullPath     = "/api/v1/threads/{thread_id}/full"
	MessagesPath = "/api/v1/threads/{thread_id}/messages"
	ToolsPath    = "/api/v1/agent/tools"
)

// codeTurnCancelling is the one refusal code of this package the apps
// branch on: the delete could not have the thread yet because its turn is
// still stopping. It is the code the turn routes use for the same state.
const codeTurnCancelling = "TURN_CANCELLING"

// What a person is told.
const (
	threadAbsent       = "Thread not found"
	stillStopping      = "This chat is still stopping its answer. Try again in a moment."
	threadDeleted      = "Thread deleted successfully"
	// defaultThreadTitle is in sentence case, as the site's labels are;
	// threads already stored as "New Chat" keep that title.
	defaultThreadTitle = "New chat"
)

// The bounds of what a request may ask for. None is a deployment's choice:
// each is the size of a column or a limit on one answer.
const (
	// maxTitleChars is new_chat_threads.title's width.
	maxTitleChars = 500
	// maxTurnIDChars is new_chat_messages.turn_id's width.
	maxTurnIDChars = 64
	// maxSearchResults bounds a search's answer: it fills a list a person
	// reads, and nobody reads past this.
	maxSearchResults = 200
	// defaultMessagePage and maxMessagePage bound the paged message list.
	defaultMessagePage = 100
	maxMessagePage     = 1000
	// maxAppendedContentBytes bounds one appended message.
	maxAppendedContentBytes = 1 << 20
)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
