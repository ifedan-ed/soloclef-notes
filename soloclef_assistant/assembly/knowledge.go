package assembly

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// KnowledgeBaseName is the specialist that changes the account's notes,
// documents and folders, as the main agent calls it. It is the Python
// chat's name for the same specialist, so a thread the Python tier began
// reads the same.
const KnowledgeBaseName = "knowledge_base"

// KnowledgeWriter is the specialist that changes the account's knowledge
// base: it writes and edits notes, renames and moves documents, makes,
// renames and moves folders, and deletes notes and empty folders, each
// change asked for first and recorded so the person can undo it from the
// chat. The API tier's own (internal/knowledgebase), handed in like the
// artifact specialist, because what it writes with (the index queue, the
// vector store) belongs to that tier.
type KnowledgeWriter interface {
	// Delegate is the specialist for one turn, run on the turn's model.
	Delegate(chat model.Model, owner KnowledgeOwner) (agent.Delegate, error)
	// Prompt is the body of the main agent's <knowledge_base> block.
	Prompt() string
	// Activities are the step display's lines for the specialist and its
	// tools, by tool name.
	Activities() map[string]Activity
}

// KnowledgeOwner is whose knowledge base a turn's specialist changes and
// where its record of each change is kept.
type KnowledgeOwner struct {
	// UserID is the account: the turn's owner, never an argument of a tool.
	UserID string
	// ThreadID is the chat thread each change is logged under, which is
	// where the person undoes it.
	ThreadID int64
	// Store is where the specialist's nested run is saved between a pause
	// for approval and the answer: the turn's own checkpoint store.
	Store checkpoint.Store
	// Trusted is asked whether a change that asks first may run without
	// asking: the account's "always allow" rules, read at each call, as the
	// main agent reads them.
	Trusted func(ctx context.Context, call tool.Call) (bool, error)
	// Notebook is the notebook a notebook chat belongs to, or nil in a
	// plain chat. A note the specialist writes there is filed in it, as
	// "Save to note" files one, and the text it reads is held to what the
	// chat may read: the notebook's sources and the notes filed in it.
	Notebook *Notebook
}

// turnKey carries a chat turn's id through the context of its run.
type turnKey struct{}

// WithTurnID is ctx carrying the id of the chat turn whose run it is: the
// id the turn's messages and stream frames already share. The chat route
// sets it on the context the loop runs in, because the agent is assembled
// before the turn's id exists and a resumed turn gets an id of its own.
// What a tool records under a turn (the knowledge base's action log, which
// "undo this turn" reads) is filed under this id.
func WithTurnID(ctx context.Context, turnID string) context.Context {
	return context.WithValue(ctx, turnKey{}, turnID)
}

// TurnIDOf is the id WithTurnID put on ctx, and whether there was one.
func TurnIDOf(ctx context.Context) (string, bool) {
	turnID, ok := ctx.Value(turnKey{}).(string)
	return turnID, ok && turnID != ""
}
