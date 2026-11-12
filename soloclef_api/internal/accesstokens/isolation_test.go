package accesstokens

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/posthogcapture"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/routeanalytics"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's database
// test. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the test is skipped.
const databaseVariable = "SOLOCLEF_API_TEST_ACCESSTOKENS_DATABASE"

type discarded struct{}

func (discarded) Capture(posthogcapture.Captured) {}

// The isolation test: an account lists its own keys and revokes its own
// keys. Revoking another account's key answers as revoking one's own does,
// so the route says nothing about whose keys exist, and the key is still
// there afterwards.
func TestAnAccountListsAndRevokesOnlyItsOwnKeys(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO personal_access_tokens (id, user_id, token_hash, token_prefix, label) VALUES
 (1, '`+alice+`', repeat('a', 64), 'ss_pat_aaaa', 'Her laptop'),
 (2, '`+bob+`',   repeat('b', 64), 'ss_pat_bbbb', 'His script')`)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Pool: pool, Tokens: Store{Pool: pool}, Analytics: routeanalytics.Capture{Sink: discarded{}},
		Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}, Logger: logger}.Register(api)
	as := func(userID, method, path string) (int, string) {
		t.Helper()
		request := httptest.NewRequest(method, path, nil)
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	left := func() int {
		var count int
		if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM personal_access_tokens`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	status, his := as(bob, http.MethodGet, ListPath)
	if status != http.StatusOK || !strings.Contains(his, "His script") || strings.Contains(his, "Her laptop") {
		t.Fatalf("his keys answered %d %s", status, his)
	}
	// Never the hash: only the short prefix a person recognises a key by.
	if strings.Contains(his, strings.Repeat("b", 64)) {
		t.Errorf("the list answered a key's hash: %s", his)
	}

	hers, _ := as(bob, http.MethodDelete, "/api/v1/pats/1")
	own, _ := as(bob, http.MethodDelete, "/api/v1/pats/2")
	if hers != own {
		t.Errorf("revoking her key answered %d and his own %d: the route tells whose keys exist", hers, own)
	}
	if left() != 1 {
		t.Fatalf("%d keys are left, want hers alone", left())
	}
	if status, raw := as(alice, http.MethodGet, ListPath); status != http.StatusOK || !strings.Contains(raw, "Her laptop") {
		t.Errorf("her key is gone: %d %s", status, raw)
	}
}
