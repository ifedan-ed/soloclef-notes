// Package notify is the one notification step. When something a person
// started finishes or fails, the code that knows it calls one method here;
// that method writes one inbox row and schedules one push. No other code
// inserts a notification for these events.
//
// # What notifies, and who calls
//
//   - A Studio output ready or failed (StudioFinished): the API's artifact
//     runner and its sweep of abandoned runs, and the worker's podcast and
//     video jobs.
//   - A source that fails to index, or a large or slow one that succeeds
//     (SourceIndexed): the indexer.
//   - An automation run that ended (AutomationFinished): the worker, beside
//     the email the run may also send.
//   - A mail draft waiting to be confirmed (MailDraftWaiting): the hook the
//     mail connector calls.
//   - A newer iPhone app in the App Store (AppUpdated): the worker's daily
//     App Store check, for the phones on an older version only. Unlike the
//     others it names the phones it is for, and it rewrites the account's
//     one update row instead of adding rows (appupdate.go says why).
//
// # The inbox row is always written; the push is decided later
//
// The row is written whatever the person's push choices are. The push is a
// message to the worker (soloclef_notify/delivery), which, when it runs,
// reads the row again and decides then: the person's switch for the kind,
// their quiet hours, whether they are looking at Soloclef right now, and
// whether they have already read the row. Deciding late is what makes a
// collapsed batch push once, with its final count.
//
// # Collapsing
//
// Some events come in bursts: ten files of one upload that all fail, a
// synced folder that cannot be read. Those carry a group, and an event of a
// group whose row is still unread and was first written inside the group's
// window grows that row ("10 imports failed") instead of adding one. Only
// the event that opens the row schedules a push, after a short wait, so the
// push says how many there were by then. Studio outputs never collapse:
// each was asked for on its own.
//
// # Payloads
//
// A push says a generic title ("Slide deck ready"), the notebook's name at
// most, and a path in the phone app, because Expo relays what it is given.
// The inbox row may say more (a file's name, why it failed): it never
// leaves this server.
//
// A notification must never fail the work it reports on. Every method
// returns its error for the caller to log, and the caller carries on.
package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
)

// Database is the pool: statements, and a transaction for a collapsing
// write.
type Database interface {
	DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Publisher puts a message on the worker's queue (produce.Producer, or the
// API's queue through an adapter).
type Publisher interface {
	Send(ctx context.Context, message produce.Message) (string, error)
}

// Notifier is the one notification step for one process.
type Notifier struct {
	Database  Database
	Publisher Publisher
	// Changes tells the person's open web tabs and app that the inbox
	// changed, so its list and badge update without a reload.
	Changes livechange.Publisher
	Log     *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

// Check reports what is missing. Called once at start-up by each tier.
func (n *Notifier) Check() error {
	if n == nil || n.Database == nil || n.Publisher == nil || n.Changes == nil || n.Log == nil {
		return errors.New("notify: the notifier needs a database, a publisher, a live-change publisher and a logger")
	}
	return nil
}

func (n *Notifier) now() time.Time {
	if n.Now == nil {
		return time.Now().UTC()
	}
	return n.Now().UTC()
}

// event is one notification, as a constructor in this package built it.
type event struct {
	userID string
	// kind is the push kind (pushpolicy), typ the inbox type.
	kind, typ string
	// title and message are the inbox's; push is what the phone is told.
	title, message string
	push           push
	metadata       map[string]any
	// group is nil for an event that never collapses.
	group *group
}

// push is what the phone is told, stored on the row as metadata.push so the
// delivery job reads exactly what this step decided.
type push struct {
	Kind  string `json:"kind"`
	Title string `json:"title"`
	Body  string `json:"body"`
	Path  string `json:"path"`
}

// record writes the event's row and schedules its push. It answers the
// row's id.
func (n *Notifier) record(ctx context.Context, e event) (int64, error) {
	if e.group == nil {
		params, err := e.insertParams(n.now())
		if err != nil {
			return 0, err
		}
		id, err := New(n.Database).InsertNotification(ctx, params)
		if err != nil {
			return 0, fmt.Errorf("notify: writing the inbox row: %w", err)
		}
		n.told(ctx, e.userID)
		return id, n.schedule(ctx, id, 0)
	}
	id, opened, err := n.collapse(ctx, e)
	if err != nil {
		return 0, err
	}
	// A grown row changed too (its count and title), so either way.
	n.told(ctx, e.userID)
	if !opened {
		// The row's own push is already scheduled (or sent); this event is
		// in its count.
		return id, nil
	}
	return id, n.schedule(ctx, id, e.group.delay)
}

// collapse grows the group's open row or opens a new one, in one
// transaction under the group's lock. opened says it made a new row.
func (n *Notifier) collapse(ctx context.Context, e event) (id int64, opened bool, err error) {
	tx, err := n.Database.Begin(ctx)
	if err != nil {
		return 0, false, fmt.Errorf("notify: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}()
	q := New(tx)
	now := n.now()
	if err = q.LockGroup(ctx, LockGroupParams{UserID: e.userID, GroupKey: e.group.key}); err != nil {
		return 0, false, fmt.Errorf("notify: locking the group: %w", err)
	}
	open, err := q.OpenGroup(ctx, OpenGroupParams{UserID: e.userID, Type: e.typ, Since: now.Add(-e.group.window), GroupKey: e.group.key})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		e.metadata["count"] = 1
		e.metadata["names"] = []string{e.group.item}
		var params InsertNotificationParams
		if params, err = e.insertParams(now); err != nil {
			return 0, false, err
		}
		var inserted int64
		if inserted, err = q.InsertNotification(ctx, params); err != nil {
			return 0, false, fmt.Errorf("notify: writing the inbox row: %w", err)
		}
		id, opened = inserted, true
	case err != nil:
		return 0, false, fmt.Errorf("notify: reading the open group: %w", err)
	default:
		grown := e.grow(open.Metadata)
		var metadata []byte
		if metadata, err = grown.encodedMetadata(); err != nil {
			return 0, false, err
		}
		if err = q.GrowGroup(ctx, GrowGroupParams{
			Title: grown.title, Message: grown.message, Metadata: metadata, Now: now, ID: open.ID, UserID: e.userID,
		}); err != nil {
			return 0, false, fmt.Errorf("notify: growing the group's row: %w", err)
		}
		id = open.ID
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, false, fmt.Errorf("notify: %w", err)
	}
	return id, opened, nil
}

// told tells the person's open clients the inbox changed: after the row is
// committed, and before the push is queued, whose failure must not keep the
// open inbox from showing the row.
func (n *Notifier) told(ctx context.Context, userID string) {
	n.Changes.Publish(ctx, userID, livechange.Change{Kind: livechange.Inbox})
}

// schedule publishes the row's push. A broker that is down loses the push,
// never the row: the person still finds it in the inbox.
func (n *Notifier) schedule(ctx context.Context, id int64, after time.Duration) error {
	if _, err := n.Publisher.Send(ctx, DeliverMessage([]int64{id}, 0, after)); err != nil {
		return fmt.Errorf("notify: the inbox row %d is written and its push could not be queued: %w", id, err)
	}
	return nil
}

func (e event) insertParams(now time.Time) (InsertNotificationParams, error) {
	metadata, err := e.encodedMetadata()
	if err != nil {
		return InsertNotificationParams{}, err
	}
	return InsertNotificationParams{UserID: e.userID, Type: e.typ, Title: e.title, Message: e.message, Metadata: metadata, Now: now}, nil
}

// encodedMetadata is the row's metadata: the event's own fields, the push,
// and the group's key, so the next event of the group finds the row.
func (e event) encodedMetadata() ([]byte, error) {
	metadata := make(map[string]any, len(e.metadata)+2)
	for key, value := range e.metadata {
		metadata[key] = value
	}
	metadata["push"] = e.push
	if e.group != nil {
		metadata["collapse_key"] = e.group.key
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("notify: encoding the row's metadata: %w", err)
	}
	return encoded, nil
}
