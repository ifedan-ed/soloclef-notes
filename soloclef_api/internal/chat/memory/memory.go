// Package memory is the chat's update_memory tool: how an answer writes the
// memory document its prompt shows.
//
// There is one document per account: what the chat remembers about the
// person. A turn is shown the asker's (chat.personBlock) and writes the
// asker's, and no other account's document can be named: the Target is the
// account the request's principal resolved to, never anything a client or
// the model sent.
//
// What a document may contain is not decided here either. The checks and
// the re-rendering are internal/markdownmemory's Save, the same function the
// Settings page's routes call, so a document saved from chat and one saved
// from Settings obey one set of rules.
//
// # What this does better than the tool it replaces
//
//   - The model passes the whole document, written from the copy in its
//     prompt. If that copy is no longer what is stored (the person edited
//     their memory in Settings while the answer was being written, or
//     another of their chats saved first), the old tool overwrote the newer
//     document with the older one plus one line. Here the stored row is
//     locked and compared with the copy this turn was shown; if they
//     differ nothing is written, and the model is handed the current
//     document to merge into.
//   - A document over the size limit is refused with the reason. The old
//     tool handed it to a second model call to shorten, unasked and
//     unseen, and saved whatever came back.
//   - The card's schema knows "saved" and "error"; every refusal used to be
//     an error result carrying the full memory document, which was then
//     saved in the message. The card here is three fields and never the
//     document (Card).
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/markdownmemory"
)

// ToolName is what the model calls, and what the page's memory card is
// keyed to.
const ToolName = assembly.MemoryToolName

// The statuses a call answers with. The page's card draws "saved" and
// "error" and draws nothing for any other, which is right for a call that
// changed nothing.
const (
	statusSaved = "saved"
	statusError = "error"
	statusNoOp  = "no_op"
)

// Target is the one document a turn may write, and the copy of it the turn's
// prompt showed the model.
type Target = assembly.MemoryTarget

// Writer makes the tool. Both fields are required.
type Writer struct {
	Pool   *pgxpool.Pool
	Logger *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (w Writer) Check() error {
	switch {
	case w.Pool == nil:
		return errors.New("memory: Pool is not set")
	case w.Logger == nil:
		return errors.New("memory: Logger is not set")
	}
	return nil
}

// result is what a call answers. The model reads all of it; the page's card
// is given the first three fields and never the rest (Card).
type result struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	// Warning is said on the card under "Memory updated": the document is
	// near its limit.
	Warning string `json:"warning,omitempty"`
	// Next and Current are for the model alone, when its copy was out of
	// date: what to do about it, and the stored document to merge into.
	Next    string  `json:"next_step,omitempty"`
	Current *string `json:"current_memory,omitempty"`
}

// shownBrackets undoes what the prompt does to a document it shows. The
// prompt replaces angle brackets so that a person's text cannot close the
// block it is shown in (chat.inert), and a model that copies the document
// back copies the replacements. Without this, every save would turn each
// "<" a person wrote into "‹" for good.
var shownBrackets = strings.NewReplacer("‹", "<", "›", ">")

// restored is the model's document with the prompt's bracket replacement
// undone. It is undone only when the stored document has neither of the two
// replacement characters itself: then every one in the model's text came
// from the prompt. A document that really contains "‹" is left exactly as
// the model wrote it, because nothing can tell its own from the prompt's.
func restored(written, stored string) string {
	if strings.ContainsAny(stored, "‹›") {
		return written
	}
	return shownBrackets.Replace(written)
}

// description is the tool as the model is offered it.
const description = "Replace the user's personal memory document, shown in <user_memory>. Pass the FULL updated Markdown document, merged and curated, never a diff or only the new line. It answers whether it was saved, or why not."

// savedMessage is for the model; the card shows its own words.
const savedMessage = "Memory updated."

// Tool returns update_memory for one turn. It is built per turn because the
// target is, and because it remembers what this turn last saw of the
// document: the copy the prompt showed, then whatever this turn saved.
func (w Writer) Tool(target Target) tool.Tool {
	var mu sync.Mutex
	known := target.Shown
	return tool.Tool{
		Name:        ToolName,
		Description: description,
		Parameters:  json.RawMessage(`{"type":"object","properties":{"updated_memory":{"type":"string","description":"The full replacement document in Markdown: entries as dated bullets under ## headings."}},"required":["updated_memory"],"additionalProperties":false}`),
		// It writes the asker's own notes, at the asker's request or about
		// what they just said; the card says it happened and the document
		// is one click away to undo. Asking before every remembered fact
		// would make memory unusable.
		Approval: tool.Auto,
		// Not Parallel: two replacements of one document have an order.
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			var args struct {
				Updated *string `json:"updated_memory"`
			}
			decoder := json.NewDecoder(strings.NewReader(string(call.Args)))
			decoder.DisallowUnknownFields()
			if err := decoder.Decode(&args); err != nil {
				return "", fmt.Errorf("the arguments do not fit the schema: %w", err)
			}
			if args.Updated == nil {
				// An absent document is not an empty one: an empty one
				// clears the memory.
				return "", errors.New("updated_memory is missing; pass the full document")
			}
			// One call at a time per turn, so that `known` is the document
			// as the last finished call left it.
			mu.Lock()
			defer mu.Unlock()
			if utf8.RuneCountInString(known) > markdownmemory.HardLimit {
				// The prompt shows at most the limit, so the model has not
				// seen the rest, and a replacement written from what it saw
				// would delete it.
				return answer(result{Status: statusError, Message: "The stored memory is longer than the " + strconv.Itoa(markdownmemory.HardLimit) + " characters the assistant is shown, so replacing it from chat would drop the rest. Nothing was saved; shorten it in Settings first."})
			}
			requested := restored(*args.Updated, known)

			type written struct {
				outcome  markdownmemory.Outcome
				stored   string
				conflict bool
			}
			done, err := database.Write(ctx, w.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (written, error) {
				stored, err := lock(ctx, tx, target)
				if err != nil {
					return written{}, err
				}
				if stored != known {
					return written{stored: stored, conflict: true}, nil
				}
				outcome := markdownmemory.Save(requested, stored)
				if !outcome.Write {
					return written{outcome: outcome, stored: stored}, nil
				}
				return written{outcome: outcome, stored: stored}, save(ctx, tx, target, outcome.Store)
			})
			if err != nil {
				if ctx.Err() != nil {
					return "", ctx.Err()
				}
				w.Logger.ErrorContext(ctx, "a memory document could not be written from chat", "error", err.Error(), "user_id", target.UserID)
				// The cause can name tables; the model gets a sentence it
				// can pass on.
				return "", errors.New("the memory could not be saved because of a storage error; nothing was changed")
			}
			switch {
			case done.conflict:
				// From now on this turn works from the document as it is.
				known = done.stored
				return answer(result{Status: statusError, Message: "The memory was changed elsewhere while this answer was being written, so nothing was saved.", Next: "current_memory is the document as it is now: merge your change into it and call " + ToolName + " again.", Current: &done.stored})
			case done.outcome.Refusal != "":
				return answer(result{Status: statusError, Message: done.outcome.Refusal})
			case !done.outcome.Write:
				return answer(result{Status: statusNoOp, Message: "No memory update requested."})
			}
			known = done.outcome.Store
			return answer(result{Status: statusSaved, Message: savedMessage, Warning: nearLimit(done.outcome.Store)})
		},
	}
}

func answer(r result) (string, error) {
	out, err := json.Marshal(r)
	return string(out), err
}

// lock reads the target's stored document and holds its row.
func lock(ctx context.Context, tx pgx.Tx, target Target) (string, error) {
	return New(tx).LockMemory(ctx, target.UserID)
}

func save(ctx context.Context, tx pgx.Tx, target Target, document string) error {
	return New(tx).SaveMemory(ctx, SaveMemoryParams{UserID: target.UserID, MemoryMd: document})
}

// nearLimit is the warning for a saved document past the soft limit, or "".
func nearLimit(document string) string {
	length := utf8.RuneCountInString(document)
	if length <= markdownmemory.SoftLimit {
		return ""
	}
	return "Memory is at " + strconv.Itoa(length) + " of " + strconv.Itoa(markdownmemory.HardLimit) + " characters. Consolidate by merging related items and removing less important entries."
}

// Usage is how full a document is, for the prompt's memory block: the model
// is told to stay within the limit, so it is told where it stands.
func Usage(document string) string {
	return strconv.Itoa(utf8.RuneCountInString(document)) + " of " + strconv.Itoa(markdownmemory.HardLimit) + " characters used"
}

// Protocol is the prompt's instructions for the tool: what belongs in the
// document, and how it is written.
const Protocol = `<memory_protocol>
<user_memory> is what you remember about the user between conversations. When a message reveals something durable about them (their role, interests, preferences, projects, background, a standing instruction), or they ask you to remember or forget something, call ` + ToolName + ` alongside your answer, in the same turn.
Pass the full document every time: the current one with your change merged in, never only the new line. Keep it under the limit shown.
Write entries as bullets "- YYYY-MM-DD: text" under ## headings such as "## Facts", "## Preferences" or "## Instructions". Use the name in <user_name> when there is one ("Ada prefers…", not "The user prefers…"); the name alone is not a memory.
Skip passing chat: one-off questions, greetings, logistics of this conversation.
</memory_protocol>

`

// CardPayload is what the page's memory card is sent, and what is saved in
// the message. A card is not only shown: it is saved, synced to every open
// page of the thread and published if the thread gets a public link, so the document
// itself is never in it.
type CardPayload struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
	Warning string `json:"warning,omitempty"`
}

// Card returns what the memory card may show of a finished call. ok is
// false for any other tool's result.
func Card(finished event.Event) (payload any, ok bool) {
	if finished.Tool != ToolName {
		return nil, false
	}
	failed := CardPayload{Status: statusError, Message: "The memory could not be updated."}
	if finished.IsError {
		// A failed call's text is this package's own sentence, but it is
		// not passed through: the rule that a card names its fields has no
		// exception for text that happens to be safe today.
		return failed, true
	}
	var decoded result
	if err := json.Unmarshal([]byte(finished.Text), &decoded); err != nil {
		return failed, true
	}
	return CardPayload{Status: decoded.Status, Message: decoded.Message, Warning: decoded.Warning}, true
}

// Protocol and Usage are the two things the turn's prompt says about
// writing memory, as the assembly asks its Remembering for them: the words
// stay beside the tool they describe.
func (Writer) Protocol() string { return Protocol }

func (Writer) Usage(document string) string { return Usage(document) }
