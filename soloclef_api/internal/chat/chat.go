// Package chat serves a chat turn: the route the web and phone apps post a
// question to and read the answer from as a stream, the route that resumes
// a turn paused for approval, the route that asks a question again or
// edited (regenerate.go), the three small routes beside them (Stop, "is a
// turn running", "what is waiting for me"), and the model menu's two
// (models.go).
//
// The loop itself is soloclef_agent: call the model, run its tools, repeat.
// This package is what turns one HTTP request into one run of that loop and
// back: whose thread it is, which tools this turn has, the rows the apps read, the
// frames they are sent, and how a turn ends when it is stopped, paused,
// fails or its reader walks away.
//
// # Whose a chat is
//
// A thread is its owner's and nobody else's. Every route begins by asking
// for the thread by its id and the caller's account in one statement
// (gate.go); another account's thread is "Thread not found", exactly as one
// that was never made. Everything a turn then reads or writes for a person
// (their instructions, memory, model, connected apps, documents, sandboxes,
// what they trust) is read by that same account id, which comes from the
// request's principal and never from the body.
//
// # The contracts kept
//
// Three things here are read by code this package does not own, and are
// kept exactly:
//
//   - The stream's frames (soloclef_agent/uistream writes them; the order
//     of this package's own frames is in run.go).
//   - The rows: new_chat_messages.content is the list of parts the apps
//     render history from (package parts), and new_chat_threads and
//     chat_session_state are synced to open pages by Zero.
//   - The request bodies and the refusals the apps branch on (THREAD_BUSY
//     and its relatives, in a "detail" object).
//
// # What this does better than the chat it replaces
//
//   - Stop, "thread busy" and turn status work with any number of API
//     processes (package turns). They were one process's memory.
//   - The step limit is per turn. A thread used to stop answering for good
//     after 120 model calls over its life.
//   - "Always allow" is a row read at the moment each call is decided, so
//     removing it takes effect on the next call, and a tool that must
//     always ask cannot be allowed for good by any route.
//   - A decision is matched to the call it answers by id, never by its
//     place in a list, and a set of decisions that does not answer exactly
//     the waiting calls is refused whole.
//   - The model searches the user's documents itself and sees the passages.
//     It used to delegate every question to a specialist and answer from
//     that specialist's summary.
//   - Nothing the model is shown on every call (the account's
//     instructions, the date) is stored in the conversation; it is built
//     per call. The memory block used to be appended to the thread again on
//     every turn.
//   - An approved call's result is saved on its card. The result used to be
//     dropped, because the resumed turn writes a new message and the card
//     is in the old one; after a reload an approved call looked
//     interrupted for ever.
//   - Each web result and each paper is its own cited source. A whole
//     search used to be one citation.
//   - A notebook chat's confinement is the server's: the notebook's
//     enabled sources are read at each turn and are the hard limit of every
//     search, an empty notebook reads nothing, and a mention outside the
//     notebook is said to be outside it (context.go, documents.Scope).
//   - The model that answers is the account's own choice from the model
//     proxy's own list, stored under the proxy's name (user_models). A model that has
//     been removed is named to the person; no other model answers in its
//     place (models.go).
//   - Asking again cuts this engine's own transcript at the turn's mark
//     and replaces the rows in one transaction (regenerate.go). It used to
//     delete the old rows in a clean-up after the stream.
//   - A long thread is summarised, so neither the cost of a question nor
//     the size of the saved state grows without bound (summary.go). The
//     summary used to tell the model its history "has been saved" to a
//     file nothing wrote.
//   - An account's connected apps can be called from the default,
//     documents-only scope, asking first. They used to be denied there,
//     which was everywhere, so they were never called.
//   - The memory the prompt shows can be written (package memory): the
//     asker's own, and only theirs. A document edited elsewhere while an
//     answer was being written is not overwritten by the answer's older
//     copy, and the document is saved in no message.
//   - A document mentioned in a notebook chat becomes one of the
//     notebook's sources before the turn's search is confined, in one
//     transaction (mentions.go). A source its owner unticked is not
//     ticked again by a mention.
//   - The model menu leaves out what the proxy says is not a chat model:
//     image, speech and embedding models (models.go, forChat). It used to
//     list every name on the proxy.
//   - A turn has a time limit. One that hangs frees its thread.
//   - A stopped turn's stream ends properly (status, finish), and what had
//     streamed is saved. It used to end mid-frame.
package chat

// The paths, as the apps call them.
const (
	TurnPath    = "/api/v1/new_chat"
	ResumePath  = "/api/v1/threads/{thread_id}/resume"
	StopPath    = "/api/v1/threads/{thread_id}/cancel-active-turn"
	StatusPath  = "/api/v1/threads/{thread_id}/turn-status"
	PendingPath = "/api/v1/threads/{thread_id}/pending-interrupts"
	// RegeneratePath asks a question again, or asks it edited.
	RegeneratePath = "/api/v1/threads/{thread_id}/regenerate"
)

// The codes the apps branch on. They travel in the refusal's "detail" object
// before a stream starts and in the "error" frame after.
const (
	codeThreadBusy       = "THREAD_BUSY"
	codeAwaitingApproval = "THREAD_AWAITING_APPROVAL"
	codeNotAwaiting      = "NOT_AWAITING_APPROVAL"
	codeTurnCancelling   = "TURN_CANCELLING"
	codeNoActiveTurn     = "NO_ACTIVE_TURN"
	codeStepLimit        = "TURN_STEP_LIMIT"
	codeTimeout          = "TURN_TIMEOUT"
	codeServerError      = "SERVER_ERROR"
)

// What a person is told.
const (
	threadAbsent      = "Thread not found"
	busyMessage       = "This chat is still answering your last message. Wait for it to finish, or stop it."
	awaitingMessage   = "This chat is waiting for your decision on an action. Approve or reject it before sending a new message."
	notAwaiting       = "Nothing in this chat is waiting for a decision."
	stepLimitMessage  = "This answer needed more steps than one turn allows, so it was stopped. What was done so far is saved; ask me to continue."
	timeoutMessage    = "This answer took too long and was stopped. What was done so far is saved; ask me to continue."
	failedMessage     = "Something went wrong while answering. Please try again."
	cancellingMessage = "Stopping the answer."
	idleMessage       = "No answer is being generated in this chat."
)

// agentThread names a chat thread's conversation in the loop's store. The
// prefix keeps chat threads apart from anything else that runs agents on the
// same table.
func agentThread(threadID int64) string {
	return "chat-" + itoa(threadID)
}
