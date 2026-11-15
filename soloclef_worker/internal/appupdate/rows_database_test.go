package appupdate

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

// device registers a phone under a sign-in that is live or not, and
// answers its id.
func device(t *testing.T, pool *pgxpool.Pool, userID, platform string, appVersion, told any, live bool) int64 {
	t.Helper()
	family := fmt.Sprintf("%08x-0000-4000-8000-%012x", time.Now().UnixNano()&0xffffffff, time.Now().UnixNano()&0xffffffffffff)
	revoked := any(nil)
	if !live {
		revoked = time.Now()
	}
	testdatabase.Exec(t, pool, `
		INSERT INTO refresh_tokens (user_id, token_hash, expires_at, revoked_at, family_id)
		VALUES ($1::uuid, md5(random()::text), now() + interval '30 days', $2, $3::uuid)`, userID, revoked, family)
	var id int64
	if err := pool.QueryRow(context.Background(), `
		INSERT INTO push_devices (user_id, token, kind, platform, app_version, update_notified_version, session_family_id)
		VALUES ($1::uuid, 'ExponentPushToken[' || md5(random()::text) || ']', 'expo', $2, $3, $4, $5::uuid)
		RETURNING id`, userID, platform, appVersion, told, family).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// The phones a pass considers: iPhones under a live sign-in that reported a
// version other than the store's and were not told about it. Not an Android
// phone, not one signed out, not one that reported no version, not one
// told already, not one on the store's own spelling of the version.
func TestThePhonesAPassConsiders(t *testing.T) {
	pool := testdatabase.Open(t)
	user := testdatabase.Account(t, pool)
	now := time.Now().UTC()
	want := device(t, pool, user, "ios", "1.1.0", nil, true)
	toldOlder := device(t, pool, user, "ios", "1.0.0", "1.1.0", true)
	device(t, pool, user, "android", "1.1.0", nil, true)
	device(t, pool, user, "ios", "1.1.0", nil, false)
	device(t, pool, user, "ios", nil, nil, true)
	device(t, pool, user, "ios", "1.1.0", "1.2.0", true)
	device(t, pool, user, "ios", "1.2.0", nil, true)

	phones, err := PoolRows{Pool: pool}.Untold(context.Background(), "1.2.0", now)
	if err != nil {
		t.Fatal(err)
	}
	var mine []Phone
	for _, phone := range phones {
		if phone.UserID == user {
			mine = append(mine, phone)
		}
	}
	if len(mine) != 2 || mine[0].ID != want || mine[0].AppVersion != "1.1.0" || mine[1].ID != toldOlder {
		t.Errorf("considered %+v; want phones %d and %d", mine, want, toldOlder)
	}
}

// The kept version: written, read back, its answer time moved alone, and
// replaced by a newer one with changed_at moving only then. A version of
// another shape is refused by the schema itself.
func TestTheKeptVersion(t *testing.T) {
	pool := testdatabase.Open(t)
	ctx := context.Background()
	rows := PoolRows{Pool: pool}
	const id = "test.appupdate.kept"
	testdatabase.Exec(t, pool, `DELETE FROM app_store_versions WHERE bundle_id = $1`, id)
	t.Cleanup(func() { testdatabase.Exec(t, pool, `DELETE FROM app_store_versions WHERE bundle_id = $1`, id) })

	if _, found, err := rows.Kept(ctx, id); err != nil || found {
		t.Fatalf("an empty table answered %v, %v", found, err)
	}
	first := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	if err := rows.Keep(ctx, id, Release{Version: version(t, "1.0"), TrackID: 42}, first); err != nil {
		t.Fatal(err)
	}
	kept, found, err := rows.Kept(ctx, id)
	if err != nil || !found || kept.Release.Version.String() != "1.0" || kept.Release.TrackID != 42 || !kept.CheckedAt.Equal(first) {
		t.Fatalf("kept %+v, %v, %v", kept, found, err)
	}
	later := first.Add(24 * time.Hour)
	if err := rows.Answered(ctx, id, later); err != nil {
		t.Fatal(err)
	}
	if err := rows.Keep(ctx, id, Release{Version: version(t, "1.0"), TrackID: 42}, later); err != nil {
		t.Fatal(err)
	}
	var changed time.Time
	if err := pool.QueryRow(ctx, `SELECT changed_at FROM app_store_versions WHERE bundle_id = $1`, id).Scan(&changed); err != nil {
		t.Fatal(err)
	}
	if !changed.Equal(first) {
		t.Errorf("the same version moved changed_at to %v", changed)
	}
	if err := rows.Keep(ctx, id, Release{Version: version(t, "1.1.0"), TrackID: 42}, later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT changed_at FROM app_store_versions WHERE bundle_id = $1`, id).Scan(&changed); err != nil {
		t.Fatal(err)
	}
	if !changed.Equal(later.Add(time.Hour)) {
		t.Errorf("a new version left changed_at at %v", changed)
	}
	if _, err := pool.Exec(ctx, `UPDATE app_store_versions SET version = '1.2.0-beta' WHERE bundle_id = $1`, id); err == nil {
		t.Error("the schema took a version that is not a plain dotted number")
	}
}
