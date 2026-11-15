package notify

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_notify/appversion"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushpolicy"
)

// AppUpdate is a newer version of the phone app in the App Store, for the
// owner's iPhones that run an older one. The worker's daily App Store check
// (soloclef_worker/internal/appupdate) chooses the phones; this step tells
// them once.
type AppUpdate struct {
	UserID string
	// Version is the store's version, as Apple writes it ("1.2.0").
	Version string
	// StoreURL is the App Store listing, which the website's inbox opens.
	// The phone opens its own copy of the listing's address, so a push
	// never carries an address the phone would open.
	StoreURL string
	// Devices are the owner's push_devices ids to tell: iPhones registered
	// with an older version.
	Devices []int64
}

// appUpdateType is the inbox row's type; the website's and the phone's
// inbox open the App Store from it.
const appUpdateType = "app_update"

// appUpdateGroup is the row's collapse key, which the delivery hands an
// iPhone as the push's collapse id: a newer version's push replaces an
// older one still on the lock screen instead of standing beside it.
const appUpdateGroup = "app:update"

// AppUpdated tells the owner's phones about the version and answers the
// phones it told now (none when every one had been told already).
//
// One transaction, under the owner's lock for this kind, does three things
// together: it marks each phone told about the version (only those not
// marked already, so two passes of the check never tell a phone twice),
// writes the account's one update row, and commits; then one delivery names
// exactly the phones just marked, so a phone on the current version, an
// Android phone and a phone told before are never pushed.
//
// The row is the account's only update row: a newer version rewrites it
// (unread, dated now) instead of adding one, so the inbox says the newest
// version and nothing that is already old. The push carries no route: the
// phone opens the App Store for a push of this kind (its pushTarget).
//
// A broker that is down loses the push, never the row or the marks, as for
// every other notification: the person still finds the update in the inbox,
// and is not pushed it again later out of order.
func (n *Notifier) AppUpdated(ctx context.Context, update AppUpdate) ([]int64, error) {
	if update.UserID == "" || len(update.Devices) == 0 || update.StoreURL == "" {
		return nil, errors.New("notify: an app update names its owner, its phones and the store's listing")
	}
	// Refused here as well as by the caller: the version is written into
	// the push and the row, and a shape the schema refuses would fail the
	// transaction after the lock was taken.
	if _, err := appversion.Parse(update.Version); err != nil {
		return nil, fmt.Errorf("notify: the store's version %q: %w", update.Version, err)
	}
	title := "Soloclef " + update.Version + " is out"
	e := event{
		userID: update.UserID, kind: pushpolicy.Update, typ: appUpdateType,
		title: title,
		// Read on the website as well as the phone, so it names no gesture.
		message: "A new version of the iPhone app is in the App Store.",
		push:    push{Kind: pushpolicy.Update, Title: title, Body: "Update it from the App Store."},
		metadata: map[string]any{
			"status": "completed", "version": update.Version, "store_url": update.StoreURL,
		},
		// Only for its collapse key (encodedMetadata): this row is rewritten
		// by AppUpdated, never grown by collapse.
		group: &group{key: appUpdateGroup},
	}
	metadata, err := e.encodedMetadata()
	if err != nil {
		return nil, err
	}

	tx, err := n.Database.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("notify: %w", err)
	}
	defer func() {
		// After a commit this is a no-op; before one it undoes the marks, so
		// a failure leaves the phones to be told by the next pass.
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()
	q := New(tx)
	now := n.now()
	if err := q.LockGroup(ctx, LockGroupParams{UserID: update.UserID, GroupKey: appUpdateGroup}); err != nil {
		return nil, fmt.Errorf("notify: locking the account's update row: %w", err)
	}
	told, err := q.ClaimUpdateDevices(ctx, ClaimUpdateDevicesParams{Version: update.Version, UserID: update.UserID, DeviceIds: update.Devices})
	if err != nil {
		return nil, fmt.Errorf("notify: marking the phones told: %w", err)
	}
	if len(told) == 0 {
		// Another pass told them already; nothing to write or push.
		return nil, nil
	}
	id, err := q.LatestAppUpdate(ctx, update.UserID)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if id, err = q.InsertNotification(ctx, InsertNotificationParams{
			UserID: update.UserID, Type: appUpdateType, Title: e.title, Message: e.message, Metadata: metadata, Now: now,
		}); err != nil {
			return nil, fmt.Errorf("notify: writing the update row: %w", err)
		}
	case err != nil:
		return nil, fmt.Errorf("notify: reading the update row: %w", err)
	default:
		if err := q.RewriteAppUpdate(ctx, RewriteAppUpdateParams{
			Title: e.title, Message: e.message, Metadata: metadata, Now: now, ID: id, UserID: update.UserID,
		}); err != nil {
			return nil, fmt.Errorf("notify: rewriting the update row: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("notify: %w", err)
	}
	if _, err := n.Publisher.Send(ctx, DeliverToMessage(id, told)); err != nil {
		return told, fmt.Errorf("notify: the update row %d is written and its push could not be queued: %w", id, err)
	}
	return told, nil
}
