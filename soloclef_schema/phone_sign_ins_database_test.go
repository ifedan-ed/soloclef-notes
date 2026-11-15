package schema_test

import (
	"context"
	"testing"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
	"git.danvics.com/danvics/soloclef/soloclef_schema/schemaversion"
)

// The two migrations that moved the phone sign-ins already in place.
const (
	// phonesFiveYears is 00009: five years from the sign-in, or a year unused.
	phonesFiveYears = 9
	// phonesWhileUsed is 00015: 180 days unused, no cap from the sign-in.
	phonesWhileUsed = 15
)

// versionBefore is the version just before `migration` among those compiled
// in: what main had numbered beneath it when it merged, so a test rolls back
// exactly that migration and the ones after it.
func versionBefore(t *testing.T, migration int64) int64 {
	t.Helper()
	versions, err := schemaversion.Expected()
	if err != nil {
		t.Fatal(err)
	}
	var previous int64
	for _, version := range versions {
		if version < migration && version > previous {
			previous = version
		}
	}
	return previous
}

// Migration 9 moves the phones already signed in to the new end -- five years
// from the sign-in, or a year from the last refresh -- and leaves alone every
// sign-in that is already over, every browser, and every spent token.
func TestPhoneSignInsMoveToFiveYearsOrAYearUnused(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	if _, err := schema.DownTo(ctx, pool, versionBefore(t, phonesFiveYears), testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", versionBefore(t, phonesFiveYears), err)
	}
	// Rows as the API wrote them under the old rule (a phone: a year from
	// its sign-in; a browser: 14 days idle under a 30-day cap).
	if _, err := pool.Exec(ctx, `
INSERT INTO "user" (id, email) VALUES ('11111111-1111-4111-8111-111111111111', 'a@example.test');
INSERT INTO refresh_tokens (user_id, token_hash, family_id, kind, signed_in_at, created_at, absolute_expiry, expires_at, revoked_at) VALUES
 -- live: signed in 100 days ago, last refreshed 10 days ago
 ('11111111-1111-4111-8111-111111111111', 'live', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'app',
  now() - interval '100 days', now() - interval '10 days', now() + interval '265 days', now() + interval '265 days', NULL),
 -- the same sign-in's token spent by that refresh
 ('11111111-1111-4111-8111-111111111111', 'spent', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'app',
  now() - interval '100 days', now() - interval '11 days', now() + interval '265 days', now() + interval '265 days', now() - interval '10 days'),
 -- signed out last week
 ('11111111-1111-4111-8111-111111111111', 'ended', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'app',
  now() - interval '50 days', now() - interval '20 days', now() + interval '315 days', now() - interval '7 days', now() - interval '7 days'),
 -- a browser
 ('11111111-1111-4111-8111-111111111111', 'web', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'web',
  now() - interval '5 days', now() - interval '1 day', now() + interval '25 days', now() + interval '13 days', NULL)`); err != nil {
		t.Fatal(err)
	}
	before := column[string](t, pool, `SELECT token_hash || ' ' || expires_at::text || ' ' || absolute_expiry::text FROM refresh_tokens WHERE token_hash <> 'live' ORDER BY token_hash`)

	// Up to this migration only: 00015 moves the same rows again.
	if _, err := schema.UpTo(ctx, pool, phonesFiveYears, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	// The live phone: five years from its sign-in, a year from its last
	// refresh (the earlier of the two).
	if moved := column[bool](t, pool, `SELECT absolute_expiry = signed_in_at + interval '1825 days'
 AND expires_at = created_at + interval '365 days' AND sso_proved_at IS NULL
 FROM refresh_tokens WHERE token_hash = 'live'`); len(moved) != 1 || !moved[0] {
		t.Error("the live phone sign-in did not move to five years, or a year unused")
	}
	if after := column[string](t, pool, `SELECT token_hash || ' ' || expires_at::text || ' ' || absolute_expiry::text FROM refresh_tokens WHERE token_hash <> 'live' ORDER BY token_hash`); !equal(before, after) {
		t.Errorf("rows that are not a live phone moved:\nbefore %v\nafter  %v", before, after)
	}

	// Rolled back, the live phone is a year from its sign-in again.
	if _, err := schema.DownTo(ctx, pool, versionBefore(t, phonesFiveYears), testKeys); err != nil {
		t.Fatalf("rolling back %d: %v", phonesFiveYears, err)
	}
	if back := column[bool](t, pool, `SELECT absolute_expiry = signed_in_at + interval '365 days' AND expires_at = absolute_expiry
 FROM refresh_tokens WHERE token_hash = 'live'`); len(back) != 1 || !back[0] {
		t.Error("rolling back did not restore the year from the sign-in")
	}
}

// Migration 15 moves the phones still signed in to "180 days from the last
// refresh, no cap", ends the ones already unused for longer, and leaves alone
// every sign-in that is already over, every browser, and every spent token.
// Rolled back, the phones still signed in are under 00009's rule again and
// the ones it ended stay ended.
func TestPhoneSignInsMoveTo180DaysUnusedWithNoCap(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	if _, err := schema.DownTo(ctx, pool, versionBefore(t, phonesWhileUsed), testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", versionBefore(t, phonesWhileUsed), err)
	}
	// Rows as the API wrote them under 00009's rule (a phone: five years from
	// its sign-in, a year from its last refresh; a browser: 14 days idle
	// under a 30-day cap).
	if _, err := pool.Exec(ctx, `
INSERT INTO "user" (id, email) VALUES ('11111111-1111-4111-8111-111111111111', 'a@example.test');
INSERT INTO refresh_tokens (user_id, token_hash, family_id, kind, signed_in_at, created_at, absolute_expiry, expires_at, revoked_at) VALUES
 -- live: signed in 400 days ago, last refreshed 10 days ago
 ('11111111-1111-4111-8111-111111111111', 'live', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'app',
  now() - interval '400 days', now() - interval '10 days', now() + interval '1425 days', now() + interval '355 days', NULL),
 -- the same sign-in's token spent by that refresh
 ('11111111-1111-4111-8111-111111111111', 'spent', 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'app',
  now() - interval '400 days', now() - interval '11 days', now() + interval '1425 days', now() + interval '354 days', now() - interval '10 days'),
 -- live under the year, but unused for 200 days since its sign-in
 ('11111111-1111-4111-8111-111111111111', 'unused', 'dddddddd-dddd-4ddd-8ddd-dddddddddddd', 'app',
  now() - interval '200 days', now() - interval '200 days', now() + interval '1625 days', now() + interval '165 days', NULL),
 -- signed out last week
 ('11111111-1111-4111-8111-111111111111', 'ended', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb', 'app',
  now() - interval '50 days', now() - interval '20 days', now() + interval '1775 days', now() - interval '7 days', now() - interval '7 days'),
 -- a browser
 ('11111111-1111-4111-8111-111111111111', 'web', 'cccccccc-cccc-4ccc-8ccc-cccccccccccc', 'web',
  now() - interval '5 days', now() - interval '1 day', now() + interval '25 days', now() + interval '13 days', NULL)`); err != nil {
		t.Fatal(err)
	}
	untouched := `SELECT token_hash || ' ' || expires_at::text || ' ' || coalesce(absolute_expiry::text, 'none')
 FROM refresh_tokens WHERE token_hash NOT IN ('live', 'unused') ORDER BY token_hash`
	before := column[string](t, pool, untouched)

	if _, err := schema.UpTo(ctx, pool, phonesWhileUsed, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	// The live phone: no cap, 180 days from its last refresh.
	if moved := column[bool](t, pool, `SELECT absolute_expiry IS NULL AND expires_at = created_at + interval '180 days'
 FROM refresh_tokens WHERE token_hash = 'live'`); len(moved) != 1 || !moved[0] {
		t.Error("the live phone sign-in did not move to 180 days unused with no cap")
	}
	// The phone unused for 200 days: ended, for the gate as for a refresh.
	if ended := column[bool](t, pool, `SELECT expires_at <= now() FROM refresh_tokens WHERE token_hash = 'unused'`); len(ended) != 1 || !ended[0] {
		t.Error("a phone unused for more than 180 days is still signed in")
	}
	if after := column[string](t, pool, untouched); !equal(before, after) {
		t.Errorf("rows that are not a live phone moved:\nbefore %v\nafter  %v", before, after)
	}

	// Rolled back: the live phone is five years from its sign-in, a year from
	// its last refresh, again; the one the migration ended stays ended.
	if _, err := schema.DownTo(ctx, pool, versionBefore(t, phonesWhileUsed), testKeys); err != nil {
		t.Fatalf("rolling back %d: %v", phonesWhileUsed, err)
	}
	if back := column[bool](t, pool, `SELECT absolute_expiry = signed_in_at + interval '1825 days'
 AND expires_at = created_at + interval '365 days'
 FROM refresh_tokens WHERE token_hash = 'live'`); len(back) != 1 || !back[0] {
		t.Error("rolling back did not restore five years, or a year unused")
	}
	if ended := column[bool](t, pool, `SELECT expires_at <= now() FROM refresh_tokens WHERE token_hash = 'unused'`); len(ended) != 1 || !ended[0] {
		t.Error("rolling back signed a phone in again")
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
