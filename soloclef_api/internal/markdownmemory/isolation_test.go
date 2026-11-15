package markdownmemory

import (
	"net/http"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's database
// tests. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the tests are skipped.
const databaseVariable = "SOLOCLEF_API_TEST_MARKDOWNMEMORY_DATABASE"

// The isolation test: there is one memory per account, the routes take no
// id, and a write or a reset made as one account leaves the other's alone.
func TestAnAccountReadsAndWritesOnlyItsOwnMemory(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `UPDATE "user" SET memory_md = E'## Facts\n- His own fact\n' WHERE id = '`+bob+`'`)
	as := routesAs(t, pool)

	// A new account remembers nothing: the empty text, never null.
	if status, raw := as(alice, http.MethodGet, UserReadPath, ""); status != http.StatusOK || !strings.Contains(raw, `"memory_md":""`) {
		t.Fatalf("her empty memory answered %d %s", status, raw)
	}
	if status, raw := as(alice, http.MethodPut, UserWritePath, `{"memory_md":"## Facts\n- Sits the bar exam in May\n"}`); status != http.StatusOK || !strings.Contains(raw, "bar exam") {
		t.Fatalf("her write answered %d %s", status, raw)
	}
	if _, his := as(bob, http.MethodGet, UserReadPath, ""); !strings.Contains(his, "His own fact") || strings.Contains(his, "bar exam") {
		t.Errorf("his memory after her write: %s", his)
	}
	// His reset empties his and leaves hers.
	if status, raw := as(bob, http.MethodPost, UserResetPath, ""); status != http.StatusOK || !strings.Contains(raw, `"memory_md":""`) {
		t.Errorf("his reset answered %d %s", status, raw)
	}
	if _, hers := as(alice, http.MethodGet, UserReadPath, ""); !strings.Contains(hers, "bar exam") {
		t.Errorf("her memory after his reset: %s", hers)
	}
}
