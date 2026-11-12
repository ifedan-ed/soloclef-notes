package notebookname

import (
	"context"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/testdatabase"
)

func TestTheNamingOverARealDatabase(t *testing.T) {
	pool := testdatabase.Open(t)
	owner := testdatabase.Account(t, pool)
	ctx := context.Background()
	store := Store{Pool: pool}

	var notebookID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO notebooks (user_id, title, title_source, icon, icon_source) VALUES ($1::uuid, 'Untitled notebook', 'auto', '📓', 'auto')
		RETURNING id`, owner).Scan(&notebookID); err != nil {
		t.Fatal(err)
	}
	// Two ready sources and one still processing; a source of another
	// notebook is not this one's.
	testdatabase.Exec(t, pool, `
		INSERT INTO documents (id, user_id, title, document_type, content, content_hash, source_markdown, status) VALUES
		 (9100001, $1::uuid, 'first.pdf', 'FILE', 'content one', 'h1', 'markdown one', '{"state":"ready"}'),
		 (9100002, $1::uuid, 'second.pdf', 'FILE', 'content two', 'h2', '', '{"state":"ready"}'),
		 (9100003, $1::uuid, 'third.pdf', 'FILE', 'content three', 'h3', NULL, '{"state":"processing"}')`, owner)
	testdatabase.Exec(t, pool, `
		INSERT INTO notebook_sources (notebook_id, user_id, document_id) VALUES
		 ($1, $2::uuid, 9100001), ($1, $2::uuid, 9100002), ($1, $2::uuid, 9100003)`, notebookID, owner)
	testdatabase.Exec(t, pool, `
		INSERT INTO notebook_outputs (notebook_id, user_id, kind, title) VALUES
		 ($1, $2::uuid, 'podcast', 'Podcast: Untitled notebook'), ($1, $2::uuid, 'report', 'My own report title')`, notebookID, owner)

	notebook, err := store.Notebook(ctx, notebookID)
	if err != nil || notebook.UserID != owner || notebook.TitleSource != "auto" {
		t.Fatalf("read %+v, %v", notebook, err)
	}
	sources, err := store.ReadySources(ctx, notebook)
	if err != nil {
		t.Fatal(err)
	}
	if sources.Ready != 2 || sources.First[0].text != "markdown one" || sources.First[1].text != "content two" {
		t.Fatalf("read %+v", sources)
	}

	naming := Naming{Title: "Neonatal Sepsis", Icon: "🩺", AutoTitle: next(0, fromSources, 2, time.Now()), ExpectedRuns: 0, OldTitle: notebook.Title}
	renamed, err := store.Rename(ctx, notebook, naming)
	if err != nil || !renamed {
		t.Fatalf("renamed=%v err=%v", renamed, err)
	}
	var title, icon string
	if err := pool.QueryRow(ctx, `SELECT title, icon FROM notebooks WHERE id = $1`, notebookID).Scan(&title, &icon); err != nil {
		t.Fatal(err)
	}
	if title != "Neonatal Sepsis" || icon != "🩺" {
		t.Fatalf("the notebook is %q %q", title, icon)
	}
	rows, err := pool.Query(ctx, `SELECT title FROM notebook_outputs WHERE notebook_id = $1 ORDER BY id`, notebookID)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for rows.Next() {
		var title string
		_ = rows.Scan(&title)
		titles = append(titles, title)
	}
	rows.Close()
	if len(titles) != 2 || titles[0] != "Podcast: Neonatal Sepsis" || titles[1] != "My own report title" {
		t.Fatalf("the outputs are %v", titles)
	}

	// A second job that decided on the old count loses: the name is
	// written once.
	if again, err := store.Rename(ctx, notebook, naming); err != nil || again {
		t.Fatalf("a stale decision renamed the notebook again: %v, %v", again, err)
	}
	// A rename by the person stops naming for good.
	testdatabase.Exec(t, pool, `UPDATE notebooks SET title = 'Mine', title_source = 'user' WHERE id = $1`, notebookID)
	if renamed, _ := store.Rename(ctx, notebook, Naming{Title: "X", AutoTitle: next(1, fromSources, 3, time.Now()), ExpectedRuns: 1}); renamed {
		t.Fatal("the person's name was written over")
	}
	// Another account cannot rename it.
	stranger := testdatabase.Account(t, pool)
	notebook.UserID = stranger
	testdatabase.Exec(t, pool, `UPDATE notebooks SET title_source = 'auto' WHERE id = $1`, notebookID)
	if renamed, _ := store.Rename(ctx, notebook, Naming{Title: "X", AutoTitle: next(1, fromSources, 3, time.Now()), ExpectedRuns: 1}); renamed {
		t.Fatal("another account renamed the notebook")
	}
}
