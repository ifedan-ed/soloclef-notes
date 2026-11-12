package accountvision

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
//	SOLOCLEF_API_TEST_ACCOUNTVISION_DATABASE=postgres://check:check@127.0.0.1:39827/check \
//	  go test ./accountvision/ -run OverARealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_ACCOUNTVISION_DATABASE"

// Ada chose a vision model and a chat model; Ben chose only a chat model.
const seed = `
TRUNCATE "user", site_settings CASCADE;
INSERT INTO "user" (id, email) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', 'ada@example.org'),
  ('bbbbbbbb-0000-4000-8000-000000000002', 'ben@example.org');
INSERT INTO user_models (user_id, purpose, model) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', 'vision', 'ada-vision'),
  ('aaaaaaaa-0000-4000-8000-000000000001', 'chat', 'ada-chat'),
  ('bbbbbbbb-0000-4000-8000-000000000002', 'chat', 'ben-chat');
`

func TestTheVisionChoiceOverARealDatabase(t *testing.T) {
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
	// An account's own vision choice; another purpose's choice is not one,
	// and an account with none has none.
	for account, want := range map[string]string{ada: "ada-vision", ben: "", cat: ""} {
		if got, err := store.VisionChoice(ctx, account); err != nil || got != want {
			t.Errorf("VisionChoice(%s) = %q, %v; want %q", account, got, err, want)
		}
	}
}
