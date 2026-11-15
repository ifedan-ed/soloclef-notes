// Package appupdate tells iPhones that a newer Soloclef is in the App Store:
// "Soloclef 1.2.0 is out", a push whose tap opens the App Store, and the same
// line in the inbox. It replaces the banner the phone app drew after asking
// Apple itself once a day: one server asking for everyone is one request a
// day instead of one per phone, and a push reaches a person who has not
// opened the app, which a banner inside it never could.
//
// # What it does, each hour
//
//  1. Asks Apple's public lookup (itunes.apple.com/lookup?bundleId=…) which
//     version the App Store offers, when the last answer is a day old
//     (Timing.LookupEvery), and keeps it in app_store_versions. An answer
//     older than one already kept never replaces it: Apple's lookup can lag
//     for a while after a release and answer the version before.
//  2. Finds the iPhones registered under a live sign-in whose app reported
//     an older version, and that have not been told about this one
//     (push_devices.update_notified_version), and tells each account's
//     phones through the one notification step (soloclef_notify/notify,
//     AppUpdated), which marks them told in the same transaction that
//     writes the inbox row. A phone is told once per store version.
//
// The hourly pass is what lets quiet hours be kept without losing the push:
// an account inside its quiet hours is skipped, not told and not marked, and
// the first pass after its quiet hours end tells it. (The delivery job would
// otherwise drop a push that lands in quiet hours, and the mark would keep
// the phone from ever being told.) An account that switched the kind off is
// told as every other kind is: the inbox row is written, and the delivery
// pushes nothing.
//
// # What it does not do
//
// Android phones are not told: the Android app has no Play Store listing,
// so there is no store version to compare with. A phone whose app does not
// report its version (a build from before it did) is not told either: that
// build draws its own banner, and nothing is guessed about a version it did
// not say.
//
// # How it is scheduled
//
// As a message that schedules its own successor, exactly as the queued-job
// reconciler and the Drive tick are (internal/jobreconcile, "How it is
// scheduled", gives the whole argument): no beat entry, a fence in Redis so
// there is one tick an hour however many workers start, and a worker seeds
// the first one as it starts.
package appupdate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

// TickTask is the hourly pass.
const TickTask = "app_store.check_update"

// Sender publishes a message (produce.Producer).
type Sender interface {
	Send(ctx context.Context, message produce.Message) (string, error)
}

// Fence lets one tick be published for a moment (RedisFence).
type Fence interface {
	// Take reports whether the caller is the first to ask for this moment.
	// The answer is remembered for hold.
	Take(ctx context.Context, moment time.Time, hold time.Duration) (bool, error)
	// Release gives a taken moment back, for a tick that could not be
	// published after all.
	Release(ctx context.Context, moment time.Time) error
}

// Store asks the App Store which version it offers (AppStore).
type Store interface {
	Lookup(ctx context.Context) (Release, error)
}

// Notifier tells one account's phones (notify.Notifier).
type Notifier interface {
	AppUpdated(ctx context.Context, update notify.AppUpdate) ([]int64, error)
}

// Timing is how the pass paces itself: decisions about the job, made in
// cmd/worker with their reasons.
type Timing struct {
	// Every is the interval between passes: how late after quiet hours end
	// a held-back account is told.
	Every time.Duration
	// LookupEvery is how old the last answer from Apple may be before Apple
	// is asked again.
	LookupEvery time.Duration
	// Accounts bounds the accounts one pass tells; the rest are the next
	// pass's.
	Accounts int
}

// Job is the hourly pass.
type Job struct {
	// BundleID is the app the App Store is asked about
	// (SOLOCLEF_WORKER_APP_STORE_BUNDLE_ID); the stored version is kept
	// under it.
	BundleID  string
	Store     Store
	Rows      Rows
	Notifier  Notifier
	Publisher Sender
	Fence     Fence
	Timing    Timing
	Log       *slog.Logger
	// Now is the clock; a test names another.
	Now func() time.Time
}

// Check reports what is missing. Called once at start-up.
func (j *Job) Check() error {
	switch {
	case j.BundleID == "":
		return errors.New("appupdate: no bundle id")
	case j.Store == nil || j.Rows == nil || j.Notifier == nil || j.Publisher == nil || j.Fence == nil || j.Log == nil || j.Now == nil:
		return errors.New("appupdate: a dependency is missing")
	case j.Timing.Every <= 0 || j.Timing.Accounts < 1:
		return errors.New("appupdate: the timing is incomplete")
	case j.Timing.LookupEvery < j.Timing.Every:
		// Asking Apple more often than the pass runs is not possible; a
		// setting that says so is a mistake.
		return errors.New("appupdate: Apple would be asked more often than the pass runs")
	}
	return nil
}

// Run is the jobs.Handler: schedule the next tick, then do this one's work.
func (j *Job) Run(ctx context.Context, _ *celery.Message) error {
	// The next tick first, so that the chain never depends on this run's
	// work succeeding, or finishing.
	scheduleErr := j.Seed(ctx)
	err := j.Pass(ctx)
	if scheduleErr != nil {
		// Without a next tick the chain would end here. The message goes
		// back and this pass happens again, which is harmless: a phone is
		// marked told as it is told, and is not told twice.
		return fmt.Errorf("%w: scheduling the next App Store check: %w", broker.ErrRequeue, scheduleErr)
	}
	return err
}

// Seed publishes the tick for the next whole interval, unless one is
// already on its way. A worker calls it once as it starts.
func (j *Job) Seed(ctx context.Context) error {
	now := j.Now()
	next := now.Truncate(j.Timing.Every).Add(j.Timing.Every)
	// Held past the moment itself: every other caller that could ask for
	// this moment asks before it comes, so two intervals covers them all
	// with room for clocks that disagree.
	won, err := j.Fence.Take(ctx, next, 2*j.Timing.Every)
	if err != nil {
		return fmt.Errorf("taking the fence for %s: %w", next.UTC().Format(time.RFC3339), err)
	}
	if !won {
		return nil
	}
	_, err = j.Publisher.Send(ctx, produce.Message{
		Task: TickTask,
		// One id a moment, so the log lines of a tick that was redelivered
		// read as the one tick it is.
		ID:           TickTask + ":" + strconv.FormatInt(next.Unix(), 10),
		Countdown:    next.Sub(now),
		IgnoreResult: true,
		// No expiry, on purpose: a tick that is dropped ends the chain.
	})
	if err != nil {
		// The fence would otherwise keep everyone else from publishing
		// this moment's tick for as long as it is held.
		if releaseErr := j.Fence.Release(context.WithoutCancel(ctx), next); releaseErr != nil {
			return fmt.Errorf("publishing the tick: %w (and its fence could not be released: %v)", err, releaseErr)
		}
		return fmt.Errorf("publishing the tick: %w", err)
	}
	return nil
}

// Pass is one hour's work: the store's version brought up to date when it
// is due, then the phones on an older one told. A lookup that failed does
// not stop the phones being told about the version already kept; both
// failures are answered together.
func (j *Job) Pass(ctx context.Context) error {
	now := j.Now()
	release, lookupErr := j.current(ctx, now)
	if release == nil {
		return lookupErr
	}
	return errors.Join(lookupErr, j.tell(ctx, *release, now))
}
