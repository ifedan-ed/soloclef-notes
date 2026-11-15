package appupdate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/appversion"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushpolicy"
)

// current is the store's version to tell phones about: the kept one, after
// asking Apple when the last answer is LookupEvery old (or there is none).
// It is nil only when nothing is kept and Apple could not say, and the
// error then says why.
func (j *Job) current(ctx context.Context, now time.Time) (*Release, error) {
	kept, found, err := j.Rows.Kept(ctx, j.BundleID)
	if err != nil {
		return nil, err
	}
	if found && now.Sub(kept.CheckedAt) < j.Timing.LookupEvery {
		return &kept.Release, nil
	}
	seen, err := j.Store.Lookup(ctx)
	if err != nil {
		// Asked again at the next pass, an hour on, since checked_at did not
		// move; meanwhile the version kept is still told.
		lookupErr := fmt.Errorf("the App Store could not be asked; it is asked again next pass: %w", err)
		if found {
			return &kept.Release, lookupErr
		}
		return nil, lookupErr
	}
	switch {
	case !found || kept.Release.Version.Older(seen.Version):
		if err := j.Rows.Keep(ctx, j.BundleID, seen, now); err != nil {
			return nil, err
		}
		j.Log.Info("the App Store offers a new version", "bundle", j.BundleID, "version", seen.Version.String())
		return &seen, nil
	case seen.Version.Older(kept.Release.Version):
		// Apple's lookup can answer the version before for a while after a
		// release. Going back would tell nobody anything true, so the newer
		// one is kept, and Apple is asked again tomorrow.
		j.Log.Info("the App Store answered an older version than one already seen; the newer is kept",
			"bundle", j.BundleID, "answered", seen.Version.String(), "kept", kept.Release.Version.String())
	}
	if err := j.Rows.Answered(ctx, j.BundleID, now); err != nil {
		return nil, err
	}
	return &kept.Release, nil
}

// tell tells each account with an iPhone on an older version, except those
// inside their quiet hours, which the first pass after them tells.
func (j *Job) tell(ctx context.Context, release Release, now time.Time) error {
	phones, err := j.Rows.Untold(ctx, release.Version.String(), now)
	if err != nil {
		return err
	}
	accounts := outdated(phones, release.Version)
	if len(accounts) == 0 {
		return nil
	}
	if len(accounts) > j.Timing.Accounts {
		// The rest are untold and unmarked, so the next pass finds them.
		accounts = accounts[:j.Timing.Accounts]
	}
	userIDs := make([]string, len(accounts))
	for i, account := range accounts {
		userIDs[i] = account.userID
	}
	stored, err := j.Rows.Preferences(ctx, userIDs)
	if err != nil {
		return err
	}
	var told, waiting int
	var failures []error
	for _, account := range accounts {
		preferences := pushpolicy.Defaults()
		if row, ok := stored[account.userID]; ok {
			var zoneErr error
			if preferences, zoneErr = pushpolicy.FromStored(row); zoneErr != nil {
				j.Log.Warn("an account's quiet hours cannot be read; ignored", "user", account.userID, "error", zoneErr.Error())
			}
		}
		// Quiet hours hold the account back for a later pass. A switch that
		// is off does not: the row is written, as for every kind, and the
		// delivery pushes nothing (pushpolicy's rule, read there).
		if pushpolicy.Decide(preferences, pushpolicy.Update, now) == pushpolicy.QuietNow {
			waiting++
			continue
		}
		phones, err := j.Notifier.AppUpdated(ctx, notify.AppUpdate{
			UserID: account.userID, Version: release.Version.String(), StoreURL: release.StoreURL(), Devices: account.phones,
		})
		if err != nil {
			// One account that cannot be told does not stop the rest; its
			// phones are still unmarked unless the row was written (then
			// only the push was lost, and the inbox has it).
			failures = append(failures, fmt.Errorf("account %s: %w", account.userID, err))
			continue
		}
		told += len(phones)
	}
	if told > 0 || waiting > 0 {
		j.Log.Info("iPhones on an older app were told about the new version",
			"version", release.Version.String(), "phones", told, "accounts_in_quiet_hours", waiting)
	}
	return errors.Join(failures...)
}

// account is one account's phones on an older version.
type account struct {
	userID string
	phones []int64
}

// outdated is the phones on a version older than the store's, by account,
// in the order the rows came (by account). A phone on the store's version
// spelled otherwise ("1.0.0" for "1.0") or on a newer one (a TestFlight
// build) is left out.
func outdated(phones []Phone, store appversion.Version) []account {
	var out []account
	for _, phone := range phones {
		installed, err := appversion.Parse(phone.AppVersion)
		if err != nil || !installed.Older(store) {
			// A stored version always parses (the schema checks its shape);
			// one that does not is not compared.
			continue
		}
		if n := len(out); n > 0 && out[n-1].userID == phone.UserID {
			out[n-1].phones = append(out[n-1].phones, phone.ID)
			continue
		}
		out = append(out, account{userID: phone.UserID, phones: []int64{phone.ID}})
	}
	return out
}
