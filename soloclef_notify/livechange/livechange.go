// Package livechange spells the live-change bus: how a tier that wrote a row
// a person can see tells that person's open clients (web tabs, the phone app
// in front) to read it again.
//
// # What travels, and what never does
//
// A change names a kind of thing and, where it is cheap, which one: "the
// account's documents changed", "thread 55 changed". It never carries the
// row. The client reloads through the REST route it already reads, which
// applies every ownership rule and every masking rule the API has; a second
// path that shipped rows would need its own copy of all of them (Zero's
// publication column lists were exactly that second copy).
//
// # Why Valkey pub/sub, and not Postgres
//
// It replaced Rocicorp Zero, which followed Postgres by logical replication.
// The other Postgres option, LISTEN/NOTIFY from triggers, would catch every
// writer for free, but a NOTIFY makes each committing transaction take one
// global lock on the notification queue (a known throughput ceiling under
// many concurrent writers, and indexing is many concurrent writers), and
// LISTEN needs a session connection that a transaction-pooling pgbouncer
// cannot give. Every tier already holds a Valkey client, and pub/sub is
// fire-and-forget fan-out, which is all a hint needs: a client that misses
// one reloads on reconnect and when it comes back to the front.
//
// Pub/sub channels belong to the server, not to a database number, so a
// tier whose URL names another database index of the same Valkey still
// reaches the same channel.
//
// # Publishing is a hint, never a failure
//
// A change is published after the write has committed (a client told
// before the commit would read the old row). If it cannot be sent the write
// still stands and the person's next reconnect or focus reloads it, so a
// publish failure is logged and never returned to the work that wrote the
// row.
//
// The module stays free of a Valkey dependency, as presence does: each tier
// hands Bus the one call it makes with its own client.
package livechange

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// Channel is the one pub/sub channel every tier publishes on and every API
// process subscribes to. One channel, not one per account: an API process
// would otherwise subscribe and unsubscribe on every connect and disconnect,
// and the cost it saves (each process reading every account's few bytes)
// is small next to that.
const Channel = "soloclef:live-changes"

// Kind is what changed, as the clients map it to the lists they reload.
// The set is the clients' contract (the web's and the phone's invalidation
// maps); a new kind needs both clients to know it, and until they do it is
// ignored there.
type Kind string

const (
	// Documents: a document (an upload, a note, a Studio-made document, a
	// connector's file) was made, removed, renamed, moved, or its indexing
	// status or stage moved.
	Documents Kind = "documents"
	// Folders: the folder tree changed.
	Folders Kind = "folders"
	// Notebooks: a notebook was made, renamed (its automatic name too),
	// removed or pinned, or its sources changed.
	Notebooks Kind = "notebooks"
	// Outputs: a Studio output, deliverable job, podcast or artifact was
	// made, moved on, finished, failed, was cancelled, renamed or removed.
	Outputs Kind = "outputs"
	// Notes: a note's text or title changed.
	Notes Kind = "notes"
	// Inbox: notifications were added, read or removed.
	Inbox Kind = "inbox"
	// Chats: a thread was made, renamed or removed, its messages were
	// saved, or a turn started or ended on it.
	Chats Kind = "chats"
	// Automations: an automation or one of its runs changed.
	Automations Kind = "automations"
	// Connectors: a connection was added, removed, changed, or finished a
	// sync.
	Connectors Kind = "connectors"
)

var kinds = map[Kind]bool{
	Documents: true, Folders: true, Notebooks: true, Outputs: true, Notes: true,
	Inbox: true, Chats: true, Automations: true, Connectors: true,
}

// Known reports whether the kind is one the clients are told about.
func Known(kind Kind) bool { return kinds[kind] }

// Change is one "read this again" for one account, as the client receives
// it. ID and NotebookID are zero when not named: they only narrow what a
// client reloads, so a writer that does not have them at hand leaves them
// out rather than reading the database again to find them.
type Change struct {
	Kind       Kind  `json:"type"`
	ID         int64 `json:"id,omitempty"`
	NotebookID int64 `json:"notebook_id,omitempty"`
}

// Message is one publish: one account's changes from one write.
type Message struct {
	UserID  string   `json:"user_id"`
	Changes []Change `json:"changes"`
}

// ErrNoOwner refuses a message that names no account: the API would deliver
// it to nobody, and a reader that took it for "everyone" would leak one
// account's activity to another.
var ErrNoOwner = errors.New("livechange: a change must name the account it belongs to")

// Encode is a message as it goes on the channel. An unknown kind is refused
// here, where a typo is the writer's bug, rather than sent to clients that
// would silently ignore it.
func Encode(userID string, changes []Change) ([]byte, error) {
	if userID == "" {
		return nil, ErrNoOwner
	}
	if len(changes) == 0 {
		return nil, errors.New("livechange: a message must carry at least one change")
	}
	for _, change := range changes {
		if !Known(change.Kind) {
			return nil, fmt.Errorf("livechange: unknown kind %q", change.Kind)
		}
	}
	return json.Marshal(Message{UserID: userID, Changes: changes})
}

// Decode reads a message off the channel. Changes of a kind this build does
// not know are dropped, not refused: during a deploy a newer worker may
// publish a kind an older API process has not heard of, and the rest of the
// message is still good.
func Decode(payload []byte) (Message, error) {
	var message Message
	if err := json.Unmarshal(payload, &message); err != nil {
		return Message{}, fmt.Errorf("livechange: %w", err)
	}
	if message.UserID == "" {
		return Message{}, ErrNoOwner
	}
	known := message.Changes[:0]
	for _, change := range message.Changes {
		if Known(change.Kind) {
			known = append(known, change)
		}
	}
	message.Changes = known
	return message, nil
}

// Publisher tells an account's open clients that something changed. It
// returns nothing on purpose: see the package comment.
type Publisher interface {
	Publish(ctx context.Context, userID string, changes ...Change)
}

// sendLimit bounds one publish. A Valkey that has stopped answering must not
// hold a request or an indexing job for longer than a blink; the change is a
// hint and the clients recover without it.
const sendLimit = 2 * time.Second

// Bus is the Publisher every tier uses, over the one call it makes with its
// own Valkey client (PUBLISH channel payload).
type Bus struct {
	Send   func(ctx context.Context, channel string, payload []byte) error
	Logger *slog.Logger
}

// Publish is Publisher. It detaches from the caller's cancellation: a request
// whose client has already gone, or a job being stopped, has still written
// its row, and the person's other clients still need to hear of it.
func (b Bus) Publish(ctx context.Context, userID string, changes ...Change) {
	payload, err := Encode(userID, changes)
	if err != nil {
		// A writer's bug (no owner, an unknown kind): loud, because it is
		// silent everywhere else.
		b.Logger.ErrorContext(ctx, "a live change could not be encoded", slog.String("error", err.Error()))
		return
	}
	sendCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sendLimit)
	defer cancel()
	if err := b.Send(sendCtx, Channel, payload); err != nil {
		b.Logger.WarnContext(ctx, "a live change could not be published; the clients reload on reconnect or focus",
			slog.String("error", err.Error()))
	}
}
