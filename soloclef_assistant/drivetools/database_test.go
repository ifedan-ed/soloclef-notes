package drivetools

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The opt-in test below asks a real Postgres what the rest of the package
// asks a stand-in. It is skipped unless the variable names a throwaway
// database at the schema soloclef_schema's `migrate up` makes; it must
// never be pointed at one anybody uses, because it empties it.
//
//	SOLOCLEF_API_TEST_DRIVETOOLS_DATABASE=postgres://check:check@127.0.0.1:39827/check \
//	  go test ./drivetools/ -run OverARealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_DRIVETOOLS_DATABASE"

// Ada has a Drive connection that is on. Ben has one he switched off.
const seed = `
TRUNCATE "user", site_settings CASCADE;
INSERT INTO "user" (id, email) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', 'ada@example.org'),
  ('bbbbbbbb-0000-4000-8000-000000000002', 'ben@example.org');
INSERT INTO search_source_connectors (id, name, connector_type, config, user_id, enabled, parser) VALUES
  (11, 'Google Drive', 'GOOGLE_DRIVE_CONNECTOR', '{"token": "ada-token", "refresh_token": "ada-refresh", "picked": ["a"]}', 'aaaaaaaa-0000-4000-8000-000000000001', true, 'llamaparse'),
  (12, 'Not Drive', 'MCP_CONNECTOR', '{"token": "other"}', 'aaaaaaaa-0000-4000-8000-000000000001', true, NULL),
  (20, 'Google Drive', 'GOOGLE_DRIVE_CONNECTOR', '{"token": "ben-token"}', 'bbbbbbbb-0000-4000-8000-000000000002', false, NULL);
`

func TestTheDriveRowsOverARealDatabase(t *testing.T) {
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, seed); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	store := Store{Pool: pool}
	config := func(id int64) (text string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT config::text FROM search_source_connectors WHERE id = $1`, id).Scan(&text); err != nil {
			t.Fatal(err)
		}
		return text
	}

	// An account's Drive connection is its own, and only while it is on.
	connection, found, err := store.Connection(ctx, ada)
	// Its parser is the one it chose, which a document read from it uses.
	if err != nil || !found || connection.ID != 11 || connection.Parser != "llamaparse" {
		t.Fatalf("Ada's connection: %+v %v %v", connection, found, err)
	}
	if _, found, err := store.Connection(ctx, ben); err != nil || found {
		t.Errorf("Ben's switched-off connection was found: %v %v", found, err)
	}

	// A renewed token is merged over the owner's own row: what else the
	// row holds is kept, and a write that names another account's row, or
	// a row that is not a Drive connection, changes nothing.
	if err := store.MergeConfig(ctx, ada, 11, []byte(`{"token": "ada-renewed", "expiry": "2026-11-12T10:00:00.000000Z"}`)); err != nil {
		t.Fatal(err)
	}
	if got := config(11); got != `{"token": "ada-renewed", "expiry": "2026-11-12T10:00:00.000000Z", "picked": ["a"], "refresh_token": "ada-refresh"}` {
		t.Errorf("Ada's row after the merge: %s", got)
	}
	for _, c := range []struct {
		owner string
		id    int64
	}{{ada, 20}, {ben, 11}, {ada, 12}} {
		if err := store.MergeConfig(ctx, c.owner, c.id, []byte(`{"token": "stolen"}`)); err != nil {
			t.Fatal(err)
		}
	}
	if config(20) != `{"token": "ben-token"}` || config(12) != `{"token": "other"}` || config(11) == `{"token": "stolen"}` {
		t.Errorf("a merge reached a row that was not its owner's Drive connection: %s / %s / %s", config(20), config(12), config(11))
	}
}
