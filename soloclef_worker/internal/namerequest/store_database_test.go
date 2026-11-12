package namerequest

import (
	"context"
	"reflect"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

// One document read by five notebooks, of which only two may still name
// themselves: the ones the lookup answers.
func TestTheNotebooksToNameOverARealDatabase(t *testing.T) {
	pool := testdatabase.Open(t)
	owner := testdatabase.Account(t, pool)
	ctx := context.Background()

	var document int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO documents (user_id, title, document_type, content, content_hash, status)
		VALUES ($1::uuid, 'ready.md', 'FILE', 'text', md5(random()::text), '{"state":"ready"}') RETURNING id`, owner).Scan(&document); err != nil {
		t.Fatal(err)
	}
	notebook := func(titleSource, autoTitle string) int64 {
		var id int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO notebooks (user_id, title, title_source, auto_title) VALUES ($1::uuid, 'Untitled notebook', $2, NULLIF($3, '')::jsonb)
			RETURNING id`, owner, titleSource, autoTitle).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	never := notebook("auto", "")
	once := notebook("auto", `{"runs": 1}`)
	spent := notebook("auto", `{"runs": 2}`)
	renamed := notebook("user", "")
	off := notebook("auto", "")
	for _, id := range []int64{never, once, spent, renamed} {
		testdatabase.Exec(t, pool, `INSERT INTO notebook_sources (notebook_id, user_id, document_id) VALUES ($1, $2::uuid, $3)`, id, owner, document)
	}
	testdatabase.Exec(t, pool, `INSERT INTO notebook_sources (notebook_id, user_id, document_id, enabled) VALUES ($1, $2::uuid, $3, false)`, off, owner, document)

	got, err := Store{Pool: pool}.ToName(ctx, document)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []int64{never, once}) {
		t.Fatalf("to name = %v, want %v: not the one that used its runs, the renamed one, or the one whose source is off", got, []int64{never, once})
	}
}
