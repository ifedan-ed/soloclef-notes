package documentreindex

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres this test may empty
// (internal/testpostgres deletes every account in it). Unset, the test is
// skipped.
const databaseVariable = "SOLOCLEF_API_TEST_REINDEX_DATABASE"

// Both statements against Postgres, with two accounts: the owner's document
// is read and handed back; the other account reads nothing and its write
// changes no row.
func TestTheReindexOverARealDatabaseKeepsTwoAccountsApart(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	ctx := context.Background()
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO documents (id, user_id, title, document_type, content, content_hash, status, parser) VALUES
 (41, '`+alice+`', 'thesis.pdf', 'FILE', '', 'hash-41', '{"state": "failed", "reason": "the parser was down"}', 'local');
INSERT INTO document_files (document_id, user_id, storage_backend, storage_key, original_filename, size_bytes) VALUES
 (41, '`+alice+`', 's3', 'documents/a/41/original/old.pdf', 'old.pdf', 10),
 (41, '`+alice+`', 's3', 'documents/a/41/original/thesis.pdf', 'thesis.pdf', 12);`)
	rows := PostgresRows{Pool: pool}
	state := func() (state, parser string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT status ->> 'state', parser FROM documents WHERE id = 41`).Scan(&state, &parser); err != nil {
			t.Fatal(err)
		}
		return state, parser
	}

	if subject, err := rows.Subject(ctx, 41, bob); err != nil || subject != nil {
		t.Errorf("another account's Subject = %+v, %v", subject, err)
	}
	if handed, err := rows.HandBack(ctx, 41, bob, "llamaparse"); err != nil || handed {
		t.Errorf("another account's HandBack = %v, %v", handed, err)
	}
	if now, parser := state(); now != "failed" || parser != "local" {
		t.Fatalf("after another account's attempt the document is %s with parser %s", now, parser)
	}

	// The newest original's name is the one a parser is matched against.
	subject, err := rows.Subject(ctx, 41, alice)
	if err != nil || subject == nil || subject.State != "failed" || subject.Filename != "thesis.pdf" || subject.HasText {
		t.Fatalf("Subject = %+v, %v", subject, err)
	}
	if handed, err := rows.HandBack(ctx, 41, alice, "llamaparse"); err != nil || !handed {
		t.Fatalf("HandBack = %v, %v", handed, err)
	}
	if now, parser := state(); now != "pending" || parser != "llamaparse" {
		t.Errorf("the document is %s with parser %s", now, parser)
	}
	// Already waiting: a second request queues no second job.
	if handed, err := rows.HandBack(ctx, 41, alice, ""); err != nil || handed {
		t.Errorf("a second HandBack = %v, %v", handed, err)
	}
}
