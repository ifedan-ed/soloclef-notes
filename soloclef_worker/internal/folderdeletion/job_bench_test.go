package folderdeletion

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/documentremoval"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

// A folder of two hundred documents of ten chunks each, removed against a
// real database and a vector store that answers at once: the job's own
// cost per document.
//
//	SOLOCLEF_WORKER_TEST_DATABASE_URL=… go test ./internal/folderdeletion/ -run '^$' -bench Folder -benchtime 3x
func BenchmarkAFolderOfTwoHundredDocuments(b *testing.B) {
	address := os.Getenv(testdatabase.Variable)
	if address == "" {
		b.Skipf("%s is not set", testdatabase.Variable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		b.Fatal(err)
	}
	defer pool.Close()
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer qdrant.Close()
	local, _ := filestore.OpenLocal(b.TempDir())
	stores, _ := filestore.NewStores(local)
	job := &Job{Pool: pool, Remover: documentremoval.Removal{Pool: pool, Files: stores,
		Vectors: documentremoval.Qdrant{URL: qdrant.URL, Alias: "chunks", HTTP: &http.Client{}}}, Attempts: 1, Backoff: time.Millisecond}

	var took time.Duration
	for n := 0; n < b.N; n++ {
		var owner string
		_ = pool.QueryRow(ctx, `INSERT INTO "user" (email) VALUES (gen_random_uuid()::text || '@bench.test') RETURNING id::text`).Scan(&owner)
		var folder int64
		_ = pool.QueryRow(ctx, `INSERT INTO folders (user_id, name, position) VALUES ($1::uuid, 'Bench', 'a0') RETURNING id`, owner).Scan(&folder)
		rows, err := pool.Query(ctx, `
			INSERT INTO documents (user_id, folder_id, title, document_type, content, content_hash, status)
			SELECT $1::uuid, $2, 'd' || i, 'FILE', 'x', 'h', '{"state":"deleting"}' FROM generate_series(1, 200) AS i
			RETURNING id`, owner, folder)
		if err != nil {
			b.Fatal(err)
		}
		var documents []any
		for rows.Next() {
			var id int64
			_ = rows.Scan(&id)
			documents = append(documents, float64(id))
		}
		rows.Close()
		if _, err := pool.Exec(ctx, `
			INSERT INTO chunks (document_id, user_id, content)
			SELECT d.id, d.user_id, 'chunk' FROM documents d, generate_series(1, 10) WHERE d.folder_id = $1`, folder); err != nil {
			b.Fatal(err)
		}
		started := time.Now()
		if err := job.Run(ctx, message(map[string]any{"user_id": owner, "folder_subtree_ids": []any{float64(folder)}}, documents...)); err != nil {
			b.Fatal(err)
		}
		took += time.Since(started)
		_, _ = pool.Exec(ctx, `DELETE FROM "user" WHERE id = $1::uuid`, owner)
	}
	b.ReportMetric(float64(took.Milliseconds())/float64(b.N)/200, "ms/document")
}
