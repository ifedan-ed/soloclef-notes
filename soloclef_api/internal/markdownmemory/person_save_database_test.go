package markdownmemory

import (
	"encoding/json"
	"net/http"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// What the Settings page sends is what a person typed, and the route keeps it:
// plain text is a 200 and is stored under Notes, merged into a Notes section
// the document already has, over a real database.
func TestThePageSavesPlainTextUnderNotes(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	as := routesAs(t, pool)
	alice := testpostgres.Alice

	put := func(memory string) (int, string) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"memory_md": memory})
		if err != nil {
			t.Fatal(err)
		}
		status, raw := as(alice, http.MethodPut, UserWritePath, string(body))
		var answer struct {
			Memory string `json:"memory_md"`
		}
		if err := json.Unmarshal([]byte(raw), &answer); err != nil {
			t.Fatalf("the answer %q: %v", raw, err)
		}
		return status, answer.Memory
	}
	stored := func() string {
		t.Helper()
		var memory string
		if err := pool.QueryRow(t.Context(), `SELECT memory_md FROM "user" WHERE id = $1`, alice).Scan(&memory); err != nil {
			t.Fatal(err)
		}
		return memory
	}

	if status, answered := put("Sits the bar exam in May"); status != http.StatusOK || answered != "## Notes\nSits the bar exam in May" {
		t.Fatalf("plain text answered %d %q", status, answered)
	}
	if stored() != "## Notes\nSits the bar exam in May" {
		t.Fatalf("plain text stored %q", stored())
	}

	// The person adds a line at the top of the saved document: it joins
	// the Notes section rather than opening a second one.
	const want = "## Notes\nPrefers short answers\nSits the bar exam in May"
	if status, answered := put("Prefers short answers\n## Notes\nSits the bar exam in May"); status != http.StatusOK || answered != want {
		t.Fatalf("a line above Notes answered %d %q", status, answered)
	}
	if stored() != want {
		t.Fatalf("a line above Notes stored %q", stored())
	}
}
