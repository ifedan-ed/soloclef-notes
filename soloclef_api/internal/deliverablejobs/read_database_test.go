package deliverablejobs

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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// The job card's read: each account reads its own jobs and no other's.
func TestAJobIsReadByItsOwnerAlone(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	testpostgres.Exec(t, pool, `
INSERT INTO deliverable_jobs (id, user_id, kind, title, tool_call_id, status, phase, progress) VALUES
 (1, '`+alice+`', 'video', 'Hers', 'a', 'running', 'rendering', 40),
 (2, '`+bob+`',   'video', 'His',  'b', 'queued', NULL, 0);`)
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	ReadRoute{Database: pool, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)
	read := func(userID, id string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/deliverable-jobs/"+id, nil)
		request = request.WithContext(principal.WithPrincipal(request.Context(),
			principal.Principal{UserID: userID, Method: principal.MethodSession}))
		recorder := httptest.NewRecorder()
		api.Adapter().ServeHTTP(recorder, request)
		return recorder
	}
	for _, c := range []struct {
		name, caller, id string
		status           int
		says             []string
	}{
		{"her own job, as its card shows it", alice, "1", 200, []string{`"title":"Hers"`, `"status":"running"`, `"phase":"rendering"`, `"progress":40`}},
		{"his own job", bob, "2", 200, []string{`"title":"His"`, `"status":"queued"`}},
		{"his job is not found for her", alice, "2", 404, []string{"not found"}},
		{"her job is not found for him", bob, "1", 404, []string{"not found"}},
		{"a job never made is the same 404", alice, "99", 404, []string{"not found"}},
		{"an id that is not a number", alice, "x", 422, []string{"valid integer"}},
	} {
		got := read(c.caller, c.id)
		for _, says := range c.says {
			if got.Code != c.status || !strings.Contains(got.Body.String(), says) {
				t.Errorf("%s: %d %s (want %d with %s)", c.name, got.Code, got.Body, c.status, says)
			}
		}
		if got.Code == 200 && got.Header().Get("Cache-Control") != "private, no-store" {
			t.Errorf("%s: cached as %q", c.name, got.Header().Get("Cache-Control"))
		}
	}
}
