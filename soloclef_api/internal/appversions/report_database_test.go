package appversions

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_APPVERSIONS_DATABASE"

// The report an operator reads before retiring a snapshot: one row per
// bundle; older bundles that report neither version nor update are counted
// together, and a phone last seen before the window is registered but not
// seen.
func TestTheReportCountsWhatPhonesReport(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	testpostgres.Exec(t, pool, `
DELETE FROM app_store_versions;
INSERT INTO app_store_versions (bundle_id, version, track_id, checked_at, changed_at) VALUES
 ('com.example.notes', '1.2.0', 42, '2026-11-13T06:00:00Z', '2026-11-12T06:00:00Z');
INSERT INTO push_devices (user_id, token, kind, platform, app_version, runtime_version, update_id, last_seen_at) VALUES
 ('`+testpostgres.Alice+`', 'ExponentPushToken[a]', 'expo', 'ios', NULL, NULL, NULL, '2026-11-13T08:00:00Z'),
 ('`+testpostgres.Bob+`', 'ExponentPushToken[b]', 'expo', 'ios', NULL, NULL, NULL, '2026-09-01T08:00:00Z'),
 ('`+testpostgres.Bob+`', 'ExponentPushToken[d]', 'expo', 'ios', '1.1.0', 'c318ec88', '0193a3c4-5e6f-7a8b-9c0d-1e2f3a4b5c6d', '2026-11-13T09:00:00Z'),
 ('`+testpostgres.Bob+`', 'ExponentPushToken[c]', 'expo', 'android', '1.2.0', NULL, NULL, '2026-11-12T08:00:00Z');`)

	var out bytes.Buffer
	if err := Write(context.Background(), pool, time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC), &out); err != nil {
		t.Fatal(err)
	}
	report := out.String()
	for _, want := range []string{
		"com.example.notes offers 1.2.0",
		"SEEN SINCE 2026-10-15",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("the report lacks %q:\n%s", want, report)
		}
	}
	// One row per bundle, read by its columns rather than its spacing.
	rows := map[string][]string{}
	for _, line := range strings.Split(report, "\n") {
		columns := regexp.MustCompile(`\s{2,}`).Split(strings.TrimSpace(line), -1)
		if len(columns) == 7 {
			rows[columns[0]+" "+columns[1]+" "+columns[3]] = columns
		}
	}
	for key, want := range map[string][2]string{
		"ios (none: an older bundle) (none: built in, or an older bundle)": {"1", "2"},
		"ios 1.1.0 0193a3c4-5e6f-7a8b-9c0d-1e2f3a4b5c6d":                   {"1", "1"},
		"android 1.2.0 (none: built in, or an older bundle)":               {"1", "1"},
	} {
		row, found := rows[key]
		if !found || row[4] != want[0] || row[5] != want[1] {
			t.Errorf("%s: %q, want seen %s of %s registered\n%s", key, row, want[0], want[1], report)
		}
	}
}
