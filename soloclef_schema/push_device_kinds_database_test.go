package schema_test

import (
	"context"
	"strings"
	"testing"

	schema "git.danvics.com/danvics/soloclef/soloclef_schema"
	"git.danvics.com/danvics/soloclef/soloclef_schema/schemaversion"
)

// pushKindsMigration is the token kinds' migration (00014).
const pushKindsMigration = 14

// versionBeneath is the version just below the migration among those
// compiled in, whatever main numbered beneath it when it merged, so a test
// rolls back exactly that migration and the ones after it.
func versionBeneath(t *testing.T, migration int64) int64 {
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

// Migration 16: every phone registered before it is an Expo token; from
// then on a registration names its kind (there is no default left to fall
// back on), an APNs token names its environment and an Expo token none.
// Rolled back, the APNs rows go (the old code would hand them to Expo) and
// the Expo ones stay.
func TestPushDevicesNameTheirTokensKind(t *testing.T) {
	pool := migrated(t)
	ctx := context.Background()
	before := versionBeneath(t, pushKindsMigration)
	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("rolling back to %d: %v", before, err)
	}
	exec(t, pool, `
INSERT INTO "user" (id, email) VALUES ('22222222-2222-4222-8222-222222222222', 'p@example.test');
INSERT INTO push_devices (user_id, token, platform) VALUES
 ('22222222-2222-4222-8222-222222222222', 'ExponentPushToken[iphone]', 'ios'),
 ('22222222-2222-4222-8222-222222222222', 'ExponentPushToken[android]', 'android')`)

	if _, err := schema.Migrate(ctx, pool, testKeys); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if kinds := column[string](t, pool, `SELECT kind || ' ' || coalesce(apns_environment, '-') FROM push_devices ORDER BY token`); strings.Join(kinds, ",") != "expo -,expo -" {
		t.Errorf("the rows from before are %v, want both Expo with no environment", kinds)
	}
	// No default is left: a registration that names no kind is a mistake.
	if _, err := pool.Exec(ctx, `INSERT INTO push_devices (user_id, token, platform) VALUES ('22222222-2222-4222-8222-222222222222', 'x', 'ios')`); err == nil ||
		!strings.Contains(err.Error(), `"kind"`) {
		t.Errorf("a row with no kind: %v", err)
	}
	insert := `INSERT INTO push_devices (user_id, token, kind, apns_environment, platform) VALUES ('22222222-2222-4222-8222-222222222222', $1, $2, $3, 'ios')`
	refused(t, pool, "ck_push_devices_kind", insert, "a", "apns", nil)
	refused(t, pool, "ck_push_devices_kind", insert, "b", "apns", "development")
	refused(t, pool, "ck_push_devices_kind", insert, "c", "expo", "production")
	refused(t, pool, "ck_push_devices_kind", insert, "d", "fcm", nil)
	exec(t, pool, insert, strings.Repeat("ab", 32), "apns", "production")
	exec(t, pool, insert, strings.Repeat("cd", 32), "apns", "sandbox")

	if _, err := schema.DownTo(ctx, pool, before, testKeys); err != nil {
		t.Fatalf("rolling back %d: %v", pushKindsMigration, err)
	}
	if tokens := column[string](t, pool, `SELECT token FROM push_devices ORDER BY token`); strings.Join(tokens, ",") != "ExponentPushToken[android],ExponentPushToken[iphone]" {
		t.Errorf("rolled back, the rows are %v; want the Expo ones only", tokens)
	}
	if columns := column[string](t, pool, `SELECT column_name::text FROM information_schema.columns WHERE table_name = 'push_devices' AND column_name IN ('kind', 'apns_environment')`); len(columns) != 0 {
		t.Errorf("rolled back, push_devices still has %v", columns)
	}
	if _, err := schema.Migrate(ctx, pool, testKeys); err != nil {
		t.Fatalf("migrating again: %v", err)
	}
}
