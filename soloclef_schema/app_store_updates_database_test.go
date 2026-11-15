package schema_test

import (
	"context"
	"testing"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
)

// appStoreUpdates is 00022, which keeps each phone's app version and the
// App Store's for the "Soloclef 1.2.0 is out" push.
const appStoreUpdates = 22

// Migration 22 adds the phone's version and the store version it was told
// about to push_devices, both empty on the phones already registered, and
// the table of the store's version. A version that is not a plain dotted
// number is refused by the schema. Rolled back, both are gone and the
// phones are as they were.
func TestAppStoreUpdatesEnterAndLeaveTheSchema(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	const ada = "11111111-1111-4111-8111-111111111111"
	before := versionBefore(t, appStoreUpdates)
	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", before, err)
	}
	exec(t, pool, `INSERT INTO "user" (id, email) VALUES ($1, 'a@example.test')`, ada)
	exec(t, pool, `INSERT INTO push_devices (user_id, token, kind, platform) VALUES ($1, 'ExponentPushToken[a]', 'expo', 'ios')`, ada)

	if _, err := schema.UpTo(ctx, pool, appStoreUpdates, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if got := column[string](t, pool, `SELECT coalesce(app_version, '-') || ' ' || coalesce(update_notified_version, '-') FROM push_devices`); got[0] != "- -" {
		t.Errorf("a phone registered before reads %q", got[0])
	}
	exec(t, pool, `UPDATE push_devices SET app_version = '1.10.0', update_notified_version = '1.2'`)
	for _, statement := range []string{
		`UPDATE push_devices SET app_version = '1.2.0-beta'`,
		`UPDATE push_devices SET app_version = ''`,
		`UPDATE push_devices SET app_version = '1.2.3.4.5'`,
		`INSERT INTO app_store_versions (bundle_id, version, track_id, checked_at, changed_at) VALUES ('x', 'v1', 1, now(), now())`,
		`INSERT INTO app_store_versions (bundle_id, version, track_id, checked_at, changed_at) VALUES ('x', '1.0', 0, now(), now())`,
	} {
		if _, err := pool.Exec(ctx, statement); err == nil {
			t.Errorf("the schema took %s", statement)
		}
	}
	exec(t, pool, `INSERT INTO app_store_versions (bundle_id, version, track_id, checked_at, changed_at) VALUES ('com.danvics.soloclef', '1.0', 6821393434, now(), now())`)

	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("rolling back %d: %v", appStoreUpdates, err)
	}
	if left := column[string](t, pool, `SELECT coalesce(to_regclass('app_store_versions')::text, '')`); left[0] != "" {
		t.Errorf("the Down left %s", left[0])
	}
	if n := column[int64](t, pool, `SELECT count(*) FROM information_schema.columns
		WHERE table_name = 'push_devices' AND column_name IN ('app_version', 'update_notified_version')`); n[0] != 0 {
		t.Errorf("the Down left %d of the columns", n[0])
	}
	if n := column[int64](t, pool, `SELECT count(*) FROM push_devices`); n[0] != 1 {
		t.Error("the Down took the phone with the columns")
	}
	if _, err := schema.Migrate(ctx, pool, testKeys); err != nil {
		t.Fatalf("migrating again: %v", err)
	}
}
