package markdownmemory

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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres for this package's database
// test. **Every account in it is deleted** (internal/testpostgres), so it
// must never name a database anyone uses. With it unset the test is skipped.
const databaseVariable = "SOLOCLEF_API_TEST_MARKDOWNMEMORY_DATABASE"

// The isolation test: there is one memory per account, the routes take no
// id, and a write or a reset made as one account leaves the other's alone.
func TestAnAccountReadsAndWritesOnlyItsOwnMemory(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `UPDATE "user" SET memory_md = E'## Facts\n- His own fact\n' WHERE id = '`+bob+`'`)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	UserRoutes{Pool: pool, Memory: Store{Pool: pool}, Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}, Logger: logger}.Register(api)

	caller := alice
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		api.Adapter().ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: caller, Method: principal.MethodSession})))
	})
	bodies := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes("PUT " + UserWritePath),
	}
	handler := requestcontext.Middleware(logger, bodies.Wrap(withCaller))
	as := func(userID, method, path, body string) (int, string) {
		t.Helper()
		caller = userID
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}

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
