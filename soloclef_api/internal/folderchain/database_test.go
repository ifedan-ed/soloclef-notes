package folderchain

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_schema/schemaversion"
)

// databaseVariable names a throwaway Postgres, the same one the folder
// routes' database test uses (internal/folders/database_test.go says how
// to start and migrate one). The test makes and removes its own accounts. With it unset the tests are skipped.
const databaseVariable = "SOLOCLEF_API_TEST_FOLDERS_DATABASE"

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Like the services, a test does not migrate: it checks, and says what
	// to run when the database is not at this tree's schema.
	if err := schemaversion.Check(ctx, pool); err != nil {
		t.Fatalf("the test database is not migrated (run soloclef_schema's `migrate up` on it first): %v", err)
	}
	return pool
}

func account(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := pool.QueryRow(context.Background(), `INSERT INTO "user" (email) VALUES ($1) RETURNING id::text`,
		"folderchain-"+hex.EncodeToString(random)+"@example.test").Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, id); err != nil {
			t.Errorf("removing the test account: %v", err)
		}
	})
	return id
}

// ensure runs Ensure in a transaction of its own and commits it.
func ensure(t *testing.T, pool *pgxpool.Pool, userID string, under *int64, chain ...string) (*int64, error) {
	t.Helper()
	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, err := Ensure(ctx, tx, userID, under, chain, time.Now().UTC())
	if err != nil {
		return nil, err
	}
	return id, tx.Commit(ctx)
}

// folderCount is how many folders an account has.
func folderCount(t *testing.T, pool *pgxpool.Pool, userID string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(context.Background(),
		`SELECT count(*) FROM folders WHERE user_id = $1::uuid`, userID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestAChainIsMadeOnceAndFoundAfterwards(t *testing.T) {
	pool := testPool(t)
	owner := account(t, pool)

	leaf, err := ensure(t, pool, owner, nil, "Shore notes", "rockpool", "limpets")
	if err != nil || leaf == nil {
		t.Fatalf("making the chain: %v", err)
	}
	if again, err := ensure(t, pool, owner, nil, "Shore notes", "rockpool", "limpets"); err != nil || *again != *leaf {
		t.Errorf("the same chain again: %v, %v; want folder %d", again, err, *leaf)
	}
	if count := folderCount(t, pool, owner); count != 3 {
		t.Errorf("%d folders, want 3", count)
	}
	// A sibling is placed after the one before it, as the folder tree
	// would place it.
	if _, err := ensure(t, pool, owner, nil, "Shore notes", "saltmarsh"); err != nil {
		t.Fatal(err)
	}
	rows, err := pool.Query(context.Background(), `
		SELECT name FROM folders
		WHERE user_id = $1::uuid AND parent_id = (SELECT id FROM folders WHERE user_id = $1::uuid AND name = 'Shore notes')
		ORDER BY position, id`, owner)
	if err != nil {
		t.Fatal(err)
	}
	if names, err := pgx.CollectRows(rows, pgx.RowTo[string]); err != nil || len(names) != 2 || names[0] != "rockpool" || names[1] != "saltmarsh" {
		t.Errorf("the siblings are %v (%v), want rockpool then saltmarsh", names, err)
	}
	// An empty chain is the folder it was told to start under.
	if same, err := ensure(t, pool, owner, leaf); err != nil || *same != *leaf {
		t.Errorf("an empty chain: %v, %v", same, err)
	}
}

// Another account's folder of the same name is not found, and is not
// built under: each account gets its own chain.
func TestAChainIsOneAccountsOwn(t *testing.T) {
	pool := testPool(t)
	a, b := account(t, pool), account(t, pool)

	mine, err := ensure(t, pool, a, nil, "Shore notes", "rockpool")
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := ensure(t, pool, b, nil, "Shore notes", "rockpool")
	if err != nil {
		t.Fatal(err)
	}
	if *mine == *theirs {
		t.Fatalf("two accounts were given the same folder, %d", *mine)
	}
	if folderCount(t, pool, a) != 2 || folderCount(t, pool, b) != 2 {
		t.Errorf("A has %d folders and B %d, want 2 each", folderCount(t, pool, a), folderCount(t, pool, b))
	}
	// B cannot start a chain under A's folder: the table refuses the
	// parent, and nothing is made.
	if planted, err := ensure(t, pool, b, mine, "planted"); err == nil {
		t.Errorf("B made folder %d under A's folder", *planted)
	}
	if folderCount(t, pool, a) != 2 || folderCount(t, pool, b) != 2 {
		t.Errorf("after the refusal A has %d folders and B %d", folderCount(t, pool, a), folderCount(t, pool, b))
	}
}

// Several requests needing the same new folder at once: one makes it, the
// rest read it, and none fails.
func TestRequestsRacingForOneFolderAllGetIt(t *testing.T) {
	pool := testPool(t)
	owner := account(t, pool)

	const racers = 8
	var (
		wait sync.WaitGroup
		ids  [racers]*int64
		errs [racers]error
	)
	for index := range racers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			ids[index], errs[index] = ensure(t, pool, owner, nil, "Uploaded together", "inner")
		}()
	}
	wait.Wait()
	for index := range racers {
		if errs[index] != nil {
			t.Fatalf("racer %d: %v", index, errs[index])
		}
		if *ids[index] != *ids[0] {
			t.Errorf("racer %d got folder %d and racer 0 got %d", index, *ids[index], *ids[0])
		}
	}
	if count := folderCount(t, pool, owner); count != 2 {
		t.Errorf("%d folders were made, want 2", count)
	}
}
