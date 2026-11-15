// Package delivery is the worker's side of push notifications: the jobs
// that send an inbox row's push to the owner's phones and read Expo's
// receipts afterwards.
//
//	notifications.deliver_push          push a batch of inbox rows
//	notifications.check_push_receipts   forget phones whose receipts say gone
//
// # When a push is sent
//
// The row is read when the job runs, not when it was written, and a push is
// sent only if all of these hold then:
//
//   - the row is unread (a person who has seen it needs no push) and says
//     what to push (metadata.push);
//   - the person has that kind switched on and it is not their quiet hours
//     (soloclef_notify/pushpolicy);
//   - they are not looking at Soloclef right now (soloclef_notify/presence);
//   - they have a phone registered under a sign-in that is still live.
//
// A delivery goes to every such phone of the owner, unless its message names
// the phones (notify.DevicesKwarg): a retry names the ones not yet reached,
// and an App Store update names the iPhones on an older version from the
// start (notify.DeliverToMessage).
//
// # Sending
//
// Each phone is pushed by the sender its token's kind names
// (push_devices.kind): an APNs token straight through Apple
// (soloclef_notify/apns), an Expo token through Expo's service
// (soloclef_notify/expo; Android phones, and an iPhone that has not
// registered again since APNs came). An APNs token of the other environment
// than the worker's (a development build signed in to the live server) is
// left alone, not sent to be refused; with no Apple key set, APNs tokens are
// not pushed at all and each delivery says so in the log.
//
// Messages go a hundred at a time per sender, a row's phones never split
// across two requests of one sender. A failure that may pass (a rate limit,
// a 5xx, a dropped connection), for a whole request or for one phone,
// republishes the job for exactly the rows and phones it did not reach, with
// a growing countdown, up to Timing.Attempts in all: the retry waits in the
// queue, never in a request, and a phone that took the push is not pushed it
// again. A token the service says is gone (Expo's DeviceNotRegistered,
// Apple's 410 or BadDeviceToken) is deleted at once. Expo's accepted tickets
// are kept, and a receipts check is scheduled, once per ReceiptDelay however
// many pushes go out, to find the phones that turned out to be gone; Apple
// answers each push at once, so there is nothing to check later.
package delivery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"

	"git.danvics.com/danvics/soloclef/soloclef_notify/apns"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushsend"
)

// Presence answers which of the accounts are looking at Soloclef now (a
// read of soloclef_notify/presence's keys, in the worker's own Valkey
// client).
type Presence interface {
	Active(ctx context.Context, userIDs []string, now time.Time) (map[string]bool, error)
}

// Fence lets one thing happen once in a span: the receipts check is
// scheduled by the first delivery of a span and not by every one. Take
// reports whether this caller won the key.
type Fence interface {
	Take(ctx context.Context, key string, hold time.Duration) (bool, error)
}

// Publisher puts a message on the queue (produce.Producer).
type Publisher interface {
	Send(ctx context.Context, message produce.Message) (string, error)
}

// Timing is how the jobs pace themselves: decisions about the jobs, made in
// cmd/worker with their reasons.
type Timing struct {
	// Attempts is how many times one row's push is tried in all.
	Attempts int
	// Backoff is the first retry's wait; each later one doubles it.
	Backoff time.Duration
	// ReceiptDelay is how long after sending the receipts are asked for.
	ReceiptDelay time.Duration
}

// ExpoSender is Expo's sender: it sends, and reads the receipts of what it
// sent.
type ExpoSender interface {
	pushsend.Sender
	pushsend.ReceiptReader
}

// Sending is what the jobs push with.
type Sending struct {
	// Expo pushes the phones registered with an Expo token and reads Expo's
	// receipts.
	Expo ExpoSender
	// APNs pushes the iPhones registered with an APNs token, or is nil when
	// the deployment has no Apple key (SOLOCLEF_WORKER_APNS_*): those phones
	// are then not pushed, and each delivery logs how many were left out.
	APNs pushsend.Sender
	// APNsEnvironment is the environment APNs pushes (apns.Production or
	// apns.Sandbox); only tokens minted in it are sent. Set exactly when
	// APNs is.
	APNsEnvironment string
	// Origin is the server's address as the phone app compares it
	// (ServerOrigin): a push names it, and the app ignores a push from a
	// server it is no longer set to.
	Origin string
}

// Jobs runs the three tasks.
type Jobs struct {
	Database  DBTX
	Sending   Sending
	Presence  Presence
	Fence     Fence
	Publisher Publisher
	Timing    Timing
	Log       *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (j *Jobs) Check() error {
	switch {
	case j.Database == nil || j.Sending.Expo == nil || j.Presence == nil || j.Fence == nil || j.Publisher == nil || j.Log == nil:
		return errors.New("delivery: a dependency is missing")
	case (j.Sending.APNs == nil) != (j.Sending.APNsEnvironment == ""):
		return errors.New("delivery: APNs and its environment are set together or not at all")
	case j.Sending.APNs != nil && j.Sending.APNsEnvironment != apns.Production && j.Sending.APNsEnvironment != apns.Sandbox:
		return fmt.Errorf("delivery: the APNs environment is %q; it is %q or %q", j.Sending.APNsEnvironment, apns.Production, apns.Sandbox)
	case j.Sending.Origin == "":
		return errors.New("delivery: the server's origin is not set")
	case j.Timing.Attempts < 1 || j.Timing.Backoff <= 0 || j.Timing.ReceiptDelay <= 0:
		return errors.New("delivery: the timing is incomplete")
	}
	return nil
}

func (j *Jobs) now() time.Time {
	if j.Now == nil {
		return time.Now().UTC()
	}
	return j.Now().UTC()
}
