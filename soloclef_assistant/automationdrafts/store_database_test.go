package automationdrafts

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// The opt-in test below puts the store to a real Postgres at the schema
// soloclef_schema's `migrate up` makes: whose a draft is, and replacing one
// only while it waits, are properties of the statements. It is skipped
// unless the variable names a throwaway database; it empties it.
//
//	SOLOCLEF_API_TEST_AUTOMATIONDRAFTS_DATABASE=postgres://… go test ./automationdrafts/ -run RealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_AUTOMATIONDRAFTS_DATABASE"

const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
)

func TestDraftsOverARealDatabase(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `
TRUNCATE "user" CASCADE;
INSERT INTO "user" (id, email) VALUES ('`+ada+`', 'ada@example.test'), ('`+ben+`', 'ben@example.test');
INSERT INTO notebooks (id, user_id, title, updated_at) VALUES
  (1, '`+ada+`', 'Older', now() - interval '1 day'), (2, '`+ada+`', 'Newer', now()), (3, '`+ben+`', 'Ben''s', now());
INSERT INTO folders (id, user_id, name, "position") VALUES (5, '`+ada+`', 'Papers', 'a0'), (6, '`+ben+`', 'Ben''s papers', 'a0');
`); err != nil {
		t.Fatal(err)
	}
	store := Store{Pool: pool}
	status := func(id string) (state string, replacedBy *string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT status, replaced_by FROM automation_drafts WHERE id = $1`, id).Scan(&state, &replacedBy); err != nil {
			t.Fatal(err)
		}
		return state, replacedBy
	}

	first, err := store.Save(ctx, ada, []byte(`{"name":"First"}`), "")
	if err != nil {
		t.Fatal(err)
	}
	if state, _ := status(first); state != "pending" {
		t.Fatalf("a new draft is %s", state)
	}
	// A newer draft replaces a waiting one, in one transaction.
	second, err := store.Save(ctx, ada, []byte(`{"name":"Second"}`), first)
	if err != nil {
		t.Fatal(err)
	}
	if state, by := status(first); state != "superseded" || by == nil || *by != second {
		t.Fatalf("the replaced draft: %s, %v", state, by)
	}
	// Replacing one that no longer waits, or another account's, saves
	// nothing at all.
	var before int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM automation_drafts`).Scan(&before)
	for name, c := range map[string]struct{ owner, replaces string }{
		"a replaced draft":   {ada, first},
		"another's draft":    {ben, second},
		"a draft never made": {ada, "99999999-9999-4999-8999-999999999999"},
	} {
		if _, err := store.Save(ctx, c.owner, []byte(`{"name":"Third"}`), c.replaces); !errors.Is(err, ErrNotWaiting) {
			t.Errorf("%s: %v", name, err)
		}
	}
	var after int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM automation_drafts`).Scan(&after)
	if after != before {
		t.Errorf("refused replacements left %d rows", after-before)
	}
	if state, _ := status(second); state != "pending" {
		t.Errorf("Ben's attempt changed Ada's draft: %s", state)
	}

	// The choices are the caller's alone, notebooks newest first.
	notebooks, err := store.Notebooks(ctx, ada)
	if err != nil || len(notebooks) != 2 || notebooks[0].Title != "Newer" || notebooks[1].Title != "Older" {
		t.Errorf("notebooks = %v, %v", notebooks, err)
	}
	folders, err := store.Folders(ctx, ada)
	if err != nil || len(folders) != 1 || folders[0] != (Choice{ID: 5, Title: "Papers"}) {
		t.Errorf("folders = %v, %v", folders, err)
	}
}
