package appupdate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_notify/appversion"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushpolicy"
)

// Kept is the store's version as app_store_versions holds it.
type Kept struct {
	Release Release
	// CheckedAt is when Apple last answered.
	CheckedAt time.Time
}

// Phone is an iPhone that may need telling: one whose app reported a
// version other than the store's, and that has not been told about it.
type Phone struct {
	ID         int64
	UserID     string
	AppVersion string
}

// Rows is the job's statements (PoolRows).
type Rows interface {
	// Kept is the version kept for the bundle id; false when none is.
	Kept(ctx context.Context, bundleID string) (Kept, bool, error)
	// Keep writes Apple's answer: the release, and when Apple answered.
	Keep(ctx context.Context, bundleID string, release Release, checkedAt time.Time) error
	// Answered moves only the time Apple last answered, for an answer that
	// changed nothing.
	Answered(ctx context.Context, bundleID string, checkedAt time.Time) error
	// Untold is every iPhone under a live sign-in whose app reported a
	// version spelled differently from the store's and that has not been
	// told about it, ordered by account.
	Untold(ctx context.Context, version string, now time.Time) ([]Phone, error)
	// Preferences is the stored push choices of the accounts that have
	// any.
	Preferences(ctx context.Context, userIDs []string) (map[string]pushpolicy.Stored, error)
}

// PoolRows is Rows over the pool.
type PoolRows struct {
	Pool *pgxpool.Pool
}

func (r PoolRows) Kept(ctx context.Context, bundleID string) (Kept, bool, error) {
	var version string
	var kept Kept
	err := r.Pool.QueryRow(ctx, `
		SELECT version, track_id, checked_at FROM app_store_versions WHERE bundle_id = $1`, bundleID).
		Scan(&version, &kept.Release.TrackID, &kept.CheckedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Kept{}, false, nil
	}
	if err != nil {
		return Kept{}, false, fmt.Errorf("appupdate: reading the kept version: %w", err)
	}
	// The schema checks the same shape (migration 00022), so this only
	// fails on a row written by hand.
	if kept.Release.Version, err = appversion.Parse(version); err != nil {
		return Kept{}, false, fmt.Errorf("appupdate: the kept version %q: %w", version, err)
	}
	return kept, true, nil
}

func (r PoolRows) Keep(ctx context.Context, bundleID string, release Release, checkedAt time.Time) error {
	// changed_at moves only when the version does: it says when the release
	// was first seen, not when Apple last confirmed it.
	_, err := r.Pool.Exec(ctx, `
		INSERT INTO app_store_versions (bundle_id, version, track_id, checked_at, changed_at)
		VALUES ($1, $2, $3, $4, $4)
		ON CONFLICT (bundle_id) DO UPDATE SET
			version = EXCLUDED.version,
			track_id = EXCLUDED.track_id,
			checked_at = EXCLUDED.checked_at,
			changed_at = CASE WHEN app_store_versions.version = EXCLUDED.version
			                  THEN app_store_versions.changed_at ELSE EXCLUDED.changed_at END`,
		bundleID, release.Version.String(), release.TrackID, checkedAt)
	if err != nil {
		return fmt.Errorf("appupdate: keeping the App Store's version: %w", err)
	}
	return nil
}

func (r PoolRows) Answered(ctx context.Context, bundleID string, checkedAt time.Time) error {
	if _, err := r.Pool.Exec(ctx, `UPDATE app_store_versions SET checked_at = $2 WHERE bundle_id = $1`, bundleID, checkedAt); err != nil {
		return fmt.Errorf("appupdate: noting the App Store's answer: %w", err)
	}
	return nil
}

func (r PoolRows) Untold(ctx context.Context, version string, now time.Time) ([]Phone, error) {
	// The version's own spelling is left out here and every other version
	// is compared in Go (appversion), because only Go reads "1.10" after
	// "1.9". Whole-table reads of narrow rows, once an hour: a deployment of
	// ten thousand iPhones reads them in milliseconds, so no index is kept
	// for it. The live-sign-in test is the delivery's own (LiveDevices in
	// soloclef_notify/delivery): a phone whose sign-in ended is not told,
	// and its account gets no inbox row for it.
	rows, err := r.Pool.Query(ctx, `
		SELECT d.id, d.user_id::text, d.app_version
		FROM push_devices d
		WHERE d.platform = 'ios'
		  AND d.app_version IS NOT NULL
		  AND d.app_version <> $1
		  AND d.update_notified_version IS DISTINCT FROM $1
		  AND (d.session_family_id IS NULL OR EXISTS (
		        SELECT 1 FROM refresh_tokens t
		        WHERE t.user_id = d.user_id
		          AND t.family_id = d.session_family_id
		          AND t.revoked_at IS NULL
		          AND t.expires_at > $2
		          AND (t.absolute_expiry IS NULL OR t.absolute_expiry > $2)))
		ORDER BY d.user_id, d.id`, version, now)
	if err != nil {
		return nil, fmt.Errorf("appupdate: reading the phones to tell: %w", err)
	}
	phones, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Phone, error) {
		var p Phone
		return p, row.Scan(&p.ID, &p.UserID, &p.AppVersion)
	})
	if err != nil {
		return nil, fmt.Errorf("appupdate: reading the phones to tell: %w", err)
	}
	return phones, nil
}

func (r PoolRows) Preferences(ctx context.Context, userIDs []string) (map[string]pushpolicy.Stored, error) {
	rows, err := r.Pool.Query(ctx, `
		SELECT user_id::text, choices, quiet_start_minute, quiet_end_minute, time_zone
		FROM push_preferences WHERE user_id = ANY ($1::uuid[])`, userIDs)
	if err != nil {
		return nil, fmt.Errorf("appupdate: reading push choices: %w", err)
	}
	defer rows.Close()
	out := make(map[string]pushpolicy.Stored, len(userIDs))
	for rows.Next() {
		var userID string
		var stored pushpolicy.Stored
		if err := rows.Scan(&userID, &stored.Choices, &stored.QuietStartMinute, &stored.QuietEndMinute, &stored.TimeZone); err != nil {
			return nil, fmt.Errorf("appupdate: reading push choices: %w", err)
		}
		out[userID] = stored
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("appupdate: reading push choices: %w", err)
	}
	return out, nil
}
