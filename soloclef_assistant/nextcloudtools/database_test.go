package nextcloudtools

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
//	SOLOCLEF_API_TEST_NEXTCLOUDTOOLS_DATABASE=postgres://check:check@127.0.0.1:39827/check \
//	  go test ./nextcloudtools/ -run OverARealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_NEXTCLOUDTOOLS_DATABASE"

// Ada has a Nextcloud connection that is on, and a Drive one. Ben has a
// Nextcloud connection he switched off.
const seed = `
TRUNCATE "user", site_settings CASCADE;
INSERT INTO "user" (id, email) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', 'ada@example.org'),
  ('bbbbbbbb-0000-4000-8000-000000000002', 'ben@example.org');
INSERT INTO search_source_connectors (id, name, connector_type, config, user_id, enabled, parser) VALUES
  (11, 'Nextcloud', 'NEXTCLOUD_CONNECTOR', '{"username": "ada"}', 'aaaaaaaa-0000-4000-8000-000000000001', true, 'datalab'),
  (12, 'Google Drive', 'GOOGLE_DRIVE_CONNECTOR', '{"token": "other"}', 'aaaaaaaa-0000-4000-8000-000000000001', true, NULL),
  (20, 'Nextcloud', 'NEXTCLOUD_CONNECTOR', '{"username": "ben"}', 'bbbbbbbb-0000-4000-8000-000000000002', false, NULL);
`

func TestTheNextcloudRowsOverARealDatabase(t *testing.T) {
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
	// An account's Nextcloud connection is its own, with the parser it
	// chose, and only while it is on.
	connection, found, err := store.Connection(ctx, ada)
	if err != nil || !found || connection.ID != 11 || connection.Parser != "datalab" || string(connection.Config) != `{"username": "ada"}` {
		t.Fatalf("Ada's connection: %+v %v %v", connection, found, err)
	}
	if _, found, err := store.Connection(ctx, ben); err != nil || found {
		t.Errorf("Ben's switched-off connection was found: %v %v", found, err)
	}
}
