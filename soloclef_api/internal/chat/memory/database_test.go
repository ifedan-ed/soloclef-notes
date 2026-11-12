package memory

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/markdownmemory"
)

// databaseVariable names a throwaway Postgres at this tier's schema (a
// pgvector/pgvector:pg17 brought up to date by soloclef_schema's
// `migrate up`). The test **truncates the tables it seeds**, so it must
// never name a database anyone uses. With it unset the tests that need it
// are skipped. It is this package's own variable because packages are
// tested side by side and each truncates what it seeds.
const databaseVariable = "SOLOCLEF_API_TEST_MEMORY_DATABASE"

// The two accounts. Each has a memory of its own; a turn of one must never
// read, replace or clear the other's.
const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
)

const (
	adasMemory = "## Facts\n- 2026-01-01: Ada prefers metric units"
	bensMemory = "## Facts\n- 2026-01-01: Ben keeps bees"
)

const seed = `
TRUNCATE "user" CASCADE;
INSERT INTO "user" (id, email, memory_md) VALUES
  ('` + ada + `', 'ada@example.org', '` + adasMemory + `'),
  ('` + ben + `', 'ben@example.org', '` + bensMemory + `');
`

func seeded(t testing.TB) *pgxpool.Pool {
	t.Helper()
	url := os.Getenv(databaseVariable)
	if url == "" {
		t.Skip(databaseVariable + " is not set")
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(context.Background(), seed); err != nil {
		t.Fatal(err)
	}
	return pool
}

func stored(t testing.TB, pool *pgxpool.Pool, query string) string {
	t.Helper()
	var text *string
	if err := pool.QueryRow(context.Background(), query).Scan(&text); err != nil {
		t.Fatal(err)
	}
	if text == nil {
		return ""
	}
	return *text
}

func adasStored(t testing.TB, pool *pgxpool.Pool) string {
	return stored(t, pool, `SELECT memory_md FROM "user" WHERE id = '`+ada+`'`)
}

func bensStored(t testing.TB, pool *pgxpool.Pool) string {
	return stored(t, pool, `SELECT memory_md FROM "user" WHERE id = '`+ben+`'`)
}

// call runs the tool once with a document and decodes what it answered.
func call(t testing.TB, made tool.Tool, document string) result {
	t.Helper()
	args, _ := json.Marshal(map[string]string{"updated_memory": document})
	text, err := made.Run(context.Background(), tool.Call{ID: "call-1", Args: args})
	if err != nil {
		t.Fatalf("the call failed: %v", err)
	}
	var out result
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("the result is not JSON: %q", text)
	}
	return out
}

func writer(pool *pgxpool.Pool) Writer {
	return Writer{Pool: pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestATurnWritesTheAskersMemoryAndOnlyThatOverARealDatabase(t *testing.T) {
	pool := seeded(t)
	made := writer(pool).Tool(Target{UserID: ada, Shown: adasMemory})

	next := adasMemory + "\n- 2026-11-11: Ada is studying for the bar exam"
	got := call(t, made, "Here is the updated memory:\n\n"+next)
	if got.Status != statusSaved || got.Message != "Memory updated." || got.Warning != "" || got.Current != nil {
		t.Fatalf("result %+v", got)
	}
	// The preamble the model wrote before the first heading is not stored.
	if adasStored(t, pool) != next {
		t.Fatalf("stored %q", adasStored(t, pool))
	}
	if bensStored(t, pool) != bensMemory {
		t.Fatalf("Ada's save reached Ben's memory: %q", bensStored(t, pool))
	}

	// A second save in the same turn works from what the first one stored,
	// not from the prompt's older copy.
	again := next + "\n- 2026-11-11: Ada reads in the mornings"
	if got := call(t, made, again); got.Status != statusSaved {
		t.Fatalf("the second save of a turn: %+v", got)
	}
	if adasStored(t, pool) != again {
		t.Fatalf("stored %q", adasStored(t, pool))
	}

	// An empty document is a deliberate clear, of the asker's own document
	// and nobody else's.
	if got := call(t, made, ""); got.Status != statusSaved || adasStored(t, pool) != "" {
		t.Fatalf("clearing: %+v, stored %q", got, adasStored(t, pool))
	}
	if bensStored(t, pool) != bensMemory {
		t.Fatalf("Ada's clear reached Ben's memory: %q", bensStored(t, pool))
	}
}

// The tool is made for one account. A turn of Ben's that was somehow shown
// Ada's document (the copy is whatever the caller passes) still writes
// Ben's row or nothing: there is no argument, in the tool's schema or in
// the document, that names another account.
func TestOneAccountsTurnCannotReachAnothersMemoryOverARealDatabase(t *testing.T) {
	pool := seeded(t)
	// Ben's tool, holding a copy of Ada's document as if it were his.
	mistaken := writer(pool).Tool(Target{UserID: ben, Shown: adasMemory})
	got := call(t, mistaken, adasMemory+"\n- 2026-11-11: Written by Ben")
	// The stored document (Ben's own) is not the copy shown, so nothing is
	// saved, and what is handed back to merge into is Ben's, never Ada's.
	if got.Status != statusError || got.Current == nil || *got.Current != bensMemory {
		t.Fatalf("a turn shown another account's document: %+v", got)
	}
	if adasStored(t, pool) != adasMemory || bensStored(t, pool) != bensMemory {
		t.Fatalf("Ada's %q, Ben's %q", adasStored(t, pool), bensStored(t, pool))
	}
	// The schema takes one argument. Anything that tries to name whose
	// memory to write is a failed call.
	for _, args := range []string{
		`{"updated_memory":"## A\n- x","user_id":"` + ada + `"}`,
		`{"updated_memory":"## A\n- x","scope":"team"}`,
		`{"updated_memory":"## A\n- x","workspace_id":7}`,
	} {
		if _, err := mistaken.Run(context.Background(), tool.Call{ID: "c", Args: json.RawMessage(args)}); err == nil {
			t.Errorf("%s was accepted", args)
		}
	}
	// Ben's own save, from his own copy, is his own row.
	bens := writer(pool).Tool(Target{UserID: ben, Shown: bensMemory})
	next := bensMemory + "\n- 2026-11-11: Ben harvested honey"
	if got := call(t, bens, next); got.Status != statusSaved || bensStored(t, pool) != next || adasStored(t, pool) != adasMemory {
		t.Fatalf("Ben's own save: %+v; Ben's %q, Ada's %q", got, bensStored(t, pool), adasStored(t, pool))
	}
	// An account that is not there has no document to write.
	gone := writer(pool).Tool(Target{UserID: "cccccccc-0000-4000-8000-000000000003", Shown: ""})
	args, _ := json.Marshal(map[string]string{"updated_memory": "## A\n- 2026-11-11: x"})
	if _, err := gone.Run(context.Background(), tool.Call{ID: "c", Args: args}); err == nil {
		t.Error("a save for an account that does not exist succeeded")
	}
}

func TestADocumentChangedElsewhereIsNotOverwrittenOverARealDatabase(t *testing.T) {
	pool := seeded(t)
	made := writer(pool).Tool(Target{UserID: ada, Shown: adasMemory})

	// Ada edits her memory in Settings while the answer is being written.
	edited := adasMemory + "\n- 2026-11-10: Ada moved to Lagos"
	if _, err := pool.Exec(context.Background(), `UPDATE "user" SET memory_md = $1 WHERE id = $2`, edited, ada); err != nil {
		t.Fatal(err)
	}
	// The model writes from the copy it was shown, which lacks that line.
	fromOldCopy := adasMemory + "\n- 2026-11-11: Ada is studying for the bar exam"
	got := call(t, made, fromOldCopy)
	if got.Status != statusError || got.Current == nil || *got.Current != edited || got.Next == "" {
		t.Fatalf("a save from an out-of-date copy: %+v", got)
	}
	if adasStored(t, pool) != edited {
		t.Fatalf("the newer document was overwritten: %q", adasStored(t, pool))
	}
	// Having been handed the current document, the model merges and saves.
	merged := edited + "\n- 2026-11-11: Ada is studying for the bar exam"
	if got := call(t, made, merged); got.Status != statusSaved || adasStored(t, pool) != merged {
		t.Fatalf("the merged save: %+v, stored %q", got, adasStored(t, pool))
	}
}

func TestWhatIsRefusedAndWhatChangesNothingOverARealDatabase(t *testing.T) {
	pool := seeded(t)
	made := writer(pool).Tool(Target{UserID: ada, Shown: adasMemory})

	for name, c := range map[string]struct{ document, status, says string }{
		"no heading": {"Ada likes tea", statusError, "at least one ## heading"},
		"too long":   {"## Facts\n" + strings.Repeat("a", markdownmemory.HardLimit), statusError, "character limit"},
		"no update":  {"NO_UPDATE", statusNoOp, "No memory update"},
	} {
		got := call(t, made, c.document)
		if got.Status != c.status || !strings.Contains(got.Message, c.says) {
			t.Errorf("%s: %+v", name, got)
		}
		if adasStored(t, pool) != adasMemory {
			t.Fatalf("%s changed the stored document: %q", name, adasStored(t, pool))
		}
	}

	// Arguments that are not the schema's are a failed call, not a clear.
	for _, args := range []string{`{}`, `{"updated_memory":null}`, `{"updated_memory":"## A\n- x","scope":"team"}`, `{"memory":"## A"}`} {
		if _, err := made.Run(context.Background(), tool.Call{ID: "c", Args: json.RawMessage(args)}); err == nil {
			t.Errorf("%s was accepted", args)
		}
	}
	if adasStored(t, pool) != adasMemory {
		t.Fatalf("a malformed call changed the stored document: %q", adasStored(t, pool))
	}
}

func TestBracketsSurviveARoundTripOverARealDatabase(t *testing.T) {
	pool := seeded(t)
	original := "## Facts\n- 2026-01-01: Ada writes Vec<T> often"
	if _, err := pool.Exec(context.Background(), `UPDATE "user" SET memory_md = $1 WHERE id = $2`, original, ada); err != nil {
		t.Fatal(err)
	}
	made := writer(pool).Tool(Target{UserID: ada, Shown: original})
	// The model copies the document as the prompt showed it, brackets
	// replaced, and adds a line.
	shown := strings.NewReplacer("<", "‹", ">", "›").Replace(original)
	if got := call(t, made, shown+"\n- 2026-11-11: Ada likes tea"); got.Status != statusSaved {
		t.Fatalf("result %+v", got)
	}
	if want := original + "\n- 2026-11-11: Ada likes tea"; adasStored(t, pool) != want {
		t.Fatalf("stored %q, want %q", adasStored(t, pool), want)
	}
}

func TestADocumentLongerThanThePromptShowsIsNotReplacedOverARealDatabase(t *testing.T) {
	pool := seeded(t)
	// Written some other way than through the checks: longer than the
	// prompt will show.
	long := "## Facts\n" + strings.Repeat("- 2026-01-01: a fact worth keeping\n", 900)
	if _, err := pool.Exec(context.Background(), `UPDATE "user" SET memory_md = $1 WHERE id = $2`, long, ada); err != nil {
		t.Fatal(err)
	}
	made := writer(pool).Tool(Target{UserID: ada, Shown: long})
	if got := call(t, made, "## Facts\n- 2026-11-11: one new fact"); got.Status != statusError || !strings.Contains(got.Message, "Nothing was saved") {
		t.Fatalf("result %+v", got)
	}
	if adasStored(t, pool) != long {
		t.Fatal("the unseen rest of the document was dropped")
	}
}

// BenchmarkSave is one update_memory call against Postgres: lock, compare,
// check, re-render, write, commit.
func BenchmarkSave(b *testing.B) {
	pool := seeded(b)
	made := writer(pool).Tool(Target{UserID: ada, Shown: adasMemory})
	documents := [2]string{adasMemory + "\n- 2026-11-11: one", adasMemory + "\n- 2026-11-11: two"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if got := call(b, made, documents[i%2]); got.Status != statusSaved {
			b.Fatalf("result %+v", got)
		}
	}
}
