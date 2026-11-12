package folderdeletion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/documentremoval"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

func TestAFolderAndItsDocumentsGoTogether(t *testing.T) {
	pool := testdatabase.Open(t)
	owner := testdatabase.Account(t, pool)
	stranger := testdatabase.Account(t, pool)
	ctx := context.Background()
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer qdrant.Close()
	local, _ := filestore.OpenLocal(t.TempDir())
	stores, _ := filestore.NewStores(local)

	var parent, child, theirs int64
	_ = pool.QueryRow(ctx, `INSERT INTO folders (user_id, name, position) VALUES ($1::uuid, 'Cardio', 'a0') RETURNING id`, owner).Scan(&parent)
	_ = pool.QueryRow(ctx, `INSERT INTO folders (user_id, parent_id, name, position) VALUES ($1::uuid, $2, 'Heart failure', 'a0') RETURNING id`, owner, parent).Scan(&child)
	_ = pool.QueryRow(ctx, `INSERT INTO folders (user_id, name, position) VALUES ($1::uuid, 'Theirs', 'a0') RETURNING id`, stranger).Scan(&theirs)
	testdatabase.Exec(t, pool, `INSERT INTO documents (id, user_id, folder_id, title, document_type, content, content_hash, status) VALUES
		(9300001, $1::uuid, $2, 'a', 'FILE', 'x', 'h', '{"state":"deleting"}'),
		(9300002, $1::uuid, $3, 'b', 'NOTE', 'x', 'h', '{"state":"deleting"}'),
		(9300003, $4::uuid, $5, 'c', 'FILE', 'x', 'h', '{"state":"ready"}')`, owner, parent, child, stranger, theirs)

	job := &Job{Pool: pool, Remover: documentremoval.Removal{Pool: pool, Files: stores,
		Vectors: documentremoval.Qdrant{URL: qdrant.URL, Alias: "chunks", HTTP: &http.Client{}}}, Attempts: 2, Backoff: time.Millisecond}
	// The stranger's document and folder are named too, as a forged message
	// would: they must survive.
	err := job.Run(ctx, message(map[string]any{"user_id": owner, "folder_subtree_ids": []any{float64(parent), float64(child), float64(theirs)}},
		float64(9300001), float64(9300002), float64(9300003)))
	if err != nil {
		t.Fatal(err)
	}
	var documents, folders int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM documents WHERE id IN (9300001, 9300002, 9300003)`).Scan(&documents)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM folders WHERE id IN ($1, $2, $3)`, parent, child, theirs).Scan(&folders)
	if documents != 1 || folders != 1 {
		t.Fatalf("%d documents and %d folders left, want the stranger's one of each", documents, folders)
	}
}
