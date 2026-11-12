package folders

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_schema/schemaversion"
)

// databaseVariable names a throwaway Postgres, already migrated, that the
// test may write to. The test makes its own accounts and removes them
// afterwards. With it unset the tests that need a database are skipped.
//
//	docker run -d --name folders-test -p 127.0.0.1::5432 -e POSTGRES_PASSWORD=check \
//	    --tmpfs /var/lib/postgresql/data pgvector/pgvector:pg17
//	url=postgres://postgres:check@127.0.0.1:<port>/postgres?sslmode=disable
//	(cd ../soloclef_schema && SOLOCLEF_MIGRATE_DATABASE_URL=$url go run ./cmd/migrate up)
//	SOLOCLEF_API_TEST_FOLDERS_DATABASE=$url \
//	    go test ./internal/folders/ ./internal/folderchain/ -count=1
const databaseVariable = "SOLOCLEF_API_TEST_FOLDERS_DATABASE"

// The same route cases as TestTheRoutes, over the real statements. This is
// the test that holds queries.sql to its first sentence: the isolation case
// passes only if every statement a route runs names the owner.
func TestTheRoutesOverARealDatabase(t *testing.T) {
	pool := testPool(t)
	routeCases(t, func(*testing.T) world { return &postgresWorld{Store: Store{Pool: pool}} })
}

// What the route cases cannot reach: the constraints themselves, asked
// directly, so the routes' reads are not what is keeping the accounts
// apart.
func TestTheTableKeepsAccountsApartByItself(t *testing.T) {
	pool := testPool(t)
	ctx := context.Background()
	w := &postgresWorld{Store: Store{Pool: pool}}
	a, b := w.account(t), w.account(t)
	at := nowOr(nil)

	mine, err := w.Insert(ctx, a, Folder{Name: "Clinic", Position: "a0", CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	// A parent of another account is refused by the composite foreign key,
	// and reported as the parent not being there.
	if _, err := w.Insert(ctx, b, Folder{Name: "Planted", Position: "a0", ParentID: &mine.ID, CreatedAt: at, UpdatedAt: at}); !errors.Is(err, ErrParentGone) {
		t.Errorf("a folder under another account's folder: %v, want ErrParentGone", err)
	}
	theirs, err := w.Insert(ctx, b, Folder{Name: "Clinic", Position: "a0", CreatedAt: at, UpdatedAt: at})
	if err != nil {
		t.Fatalf("the same top-level name in another account: %v", err)
	}
	if err := w.Reparent(ctx, b, theirs.ID, &mine.ID, "a1", at); !errors.Is(err, ErrParentGone) {
		t.Errorf("a move under another account's folder: %v, want ErrParentGone", err)
	}
	// Two top-level folders of one name in one account: NULL parents are
	// compared as equal, which a plain unique index would not do.
	if _, err := w.Insert(ctx, a, Folder{Name: "Clinic", Position: "a1", CreatedAt: at, UpdatedAt: at}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("a second top-level folder of the same name: %v, want ErrNameTaken", err)
	}

	// Every write that names a row of A's as B changes nothing.
	document := w.addDocument(t, a, &mine.ID, "ready")
	for why, write := range map[string]func() error{
		"rename":       func() error { return w.Rename(ctx, b, mine.ID, "Taken", at) },
		"reparent":     func() error { return w.Reparent(ctx, b, mine.ID, nil, "zz", at) },
		"reposition":   func() error { return w.Reposition(ctx, b, mine.ID, "zz", at) },
		"clear":        func() error { return w.ClearWatched(ctx, b, mine.ID, at) },
		"move":         func() error { return w.MoveDocuments(ctx, b, []int64{document}, nil) },
		"move to mine": func() error { return w.MoveDocuments(ctx, b, []int64{document}, &theirs.ID) },
		"restore": func() error {
			return w.RestoreMarked(ctx, b, Marked{Documents: []MarkedDocument{{ID: document, Status: []byte(`{"state": "failed"}`)}}})
		},
	} {
		if err := write(); err != nil {
			t.Errorf("%s: %v", why, err)
		}
	}
	marked, err := w.MarkSubtreeDeleting(ctx, b, mine.ID)
	if err != nil || len(marked.SubtreeIDs) != 0 || len(marked.Documents) != 0 {
		t.Errorf("B marking A's folder: %+v, %v", marked, err)
	}
	if after := w.folderRow(t, mine.ID); after.Name != "Clinic" || after.Position != "a0" || after.ParentID != nil {
		t.Errorf("A's folder is now %+v", after)
	}
	if folderID, state, _ := w.document(t, document); folderID == nil || *folderID != mine.ID || state != "ready" {
		t.Errorf("A's document is now in %v, %s", folderID, state)
	}
	for why, read := range map[string]func() (int, error){
		"ancestors": func() (int, error) { chain, err := w.Ancestors(ctx, b, mine.ID); return len(chain), err },
		"depth":     func() (int, error) { return w.Depth(ctx, b, mine.ID) },
		"documents": func() (int, error) { found, err := w.Documents(ctx, b, []int64{document}); return len(found), err },
	} {
		if count, err := read(); err != nil || count != 0 {
			t.Errorf("B reading A's %s: %d, %v", why, count, err)
		}
	}
}

// testPool connects to the database the variable names, migrated.
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

// postgresWorld is Store with the test's own ways in and out.
type postgresWorld struct{ Store }

var _ world = (*postgresWorld)(nil)

func (w *postgresWorld) account(t *testing.T) string {
	t.Helper()
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	var id string
	err := w.Pool.QueryRow(context.Background(),
		`INSERT INTO "user" (email) VALUES ($1) RETURNING id::text`,
		"folders-"+hex.EncodeToString(random)+"@example.test").Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	// The account's folders and documents go with it, by cascade.
	t.Cleanup(func() {
		if _, err := w.Pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1::uuid`, id); err != nil {
			t.Errorf("removing the test account: %v", err)
		}
	})
	return id
}

func (w *postgresWorld) addDocument(t *testing.T, userID string, folderID *int64, state string) int64 {
	t.Helper()
	var id int64
	err := w.Pool.QueryRow(context.Background(), `
		INSERT INTO documents (user_id, title, document_type, content, content_hash, folder_id, status)
		VALUES ($1::uuid, 'Document', 'FILE', '', md5(random()::text), $2, jsonb_build_object('state', $3::text))
		RETURNING id`, userID, folderID, state).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (w *postgresWorld) document(t *testing.T, documentID int64) (*int64, string, bool) {
	t.Helper()
	var (
		folderID *int64
		state    string
	)
	err := w.Pool.QueryRow(context.Background(),
		`SELECT folder_id, status ->> 'state' FROM documents WHERE id = $1`, documentID).Scan(&folderID, &state)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, "", false
	}
	if err != nil {
		t.Fatal(err)
	}
	return folderID, state, true
}

func (w *postgresWorld) setMetadata(t *testing.T, folderID int64, metadata string) {
	t.Helper()
	if _, err := w.Pool.Exec(context.Background(),
		`UPDATE folders SET metadata = $2::jsonb WHERE id = $1`, folderID, metadata); err != nil {
		t.Fatal(err)
	}
}

func (w *postgresWorld) folderRow(t *testing.T, folderID int64) *Folder {
	t.Helper()
	var folder Folder
	err := w.Pool.QueryRow(context.Background(), `
		SELECT id, name, position, parent_id, created_at, updated_at, metadata
		FROM folders WHERE id = $1`, folderID).
		Scan(&folder.ID, &folder.Name, &folder.Position, &folder.ParentID, &folder.CreatedAt, &folder.UpdatedAt, &folder.Metadata)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	return &folder
}
