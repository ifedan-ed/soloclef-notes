package accountprofile

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
const databaseVariable = "SOLOCLEF_API_TEST_ACCOUNTPROFILE_DATABASE"

// The isolation test for the profile and the settings. Neither takes an id:
// the caller is the whole predicate. So what is proved is that a change made
// as one account is made to that account's row and to no other, and that
// each account reads its own.
func TestAnAccountReadsAndChangesOnlyItsOwnProfileAndSettings(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	store := Store{Pool: pool}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	envelope := errorenvelope.Writer{ReportURL: "https://notes.example/contact"}
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	ReadRoute{Profiles: store, Errors: envelope, Logger: logger}.Register(api)
	UpdateRoute{Pool: pool, Errors: envelope, Logger: logger}.Register(api)
	SettingsRoutes{Settings: store, Errors: envelope, Logger: logger}.Register(api)

	caller := principal.Principal{UserID: alice, Method: principal.MethodSession}
	withCaller := http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		api.Adapter().ServeHTTP(response, request.WithContext(principal.WithPrincipal(request.Context(), caller)))
	})
	bodies := requestbody.Middleware{
		Covers: requestbody.CoveringRoutes("PATCH "+UpdatePath, "PATCH "+SettingsUpdatePath),
	}
	handler := requestcontext.Middleware(logger, bodies.Wrap(withCaller))
	as := func(who principal.Principal, method, path, body string) (int, string) {
		t.Helper()
		caller = who
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			request.Header.Set("Content-Type", "application/json")
		}
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)
		return recorder.Code, recorder.Body.String()
	}
	her := principal.Principal{UserID: alice, Method: principal.MethodSession}
	him := principal.Principal{UserID: bob, Method: principal.MethodSession}

	// A new account's settings: no instructions, citations on, and API
	// access OFF, which is the one access control an account has.
	fresh := `{"chat_instructions":"","citations_enabled":true,"api_access_enabled":false}`
	for _, who := range []principal.Principal{her, him} {
		if status, raw := as(who, http.MethodGet, SettingsReadPath, ""); status != http.StatusOK || raw != fresh {
			t.Fatalf("a new account's settings answered %d %s", status, raw)
		}
	}

	// She changes two of the three. Hers change; his do not.
	status, changed := as(her, http.MethodPatch, SettingsUpdatePath, `{"api_access_enabled":true,"chat_instructions":"Answer in French."}`)
	want := `{"chat_instructions":"Answer in French.","citations_enabled":true,"api_access_enabled":true}`
	if status != http.StatusOK || changed != want {
		t.Fatalf("the change answered %d %s", status, changed)
	}
	if _, raw := as(her, http.MethodGet, SettingsReadPath, ""); raw != want {
		t.Errorf("her settings read %s", raw)
	}
	if _, raw := as(him, http.MethodGet, SettingsReadPath, ""); raw != fresh {
		t.Errorf("his settings changed with hers: %s", raw)
	}
	// What is not mentioned is not written; null clears the instructions;
	// an empty body changes nothing.
	if _, raw := as(her, http.MethodPatch, SettingsUpdatePath, `{"citations_enabled":false,"chat_instructions":null}`); raw != `{"chat_instructions":"","citations_enabled":false,"api_access_enabled":true}` {
		t.Errorf("the second change answered %s", raw)
	}
	if _, raw := as(her, http.MethodPatch, SettingsUpdatePath, `{}`); raw != `{"chat_instructions":"","citations_enabled":false,"api_access_enabled":true}` {
		t.Errorf("an empty change answered %s", raw)
	}
	if status, raw := as(her, http.MethodPatch, SettingsUpdatePath, `{"api_access_enabled":"perhaps"}`); status != http.StatusUnprocessableEntity {
		t.Errorf("a switch that is not a boolean answered %d %s", status, raw)
	}

	// A key or an agent may read the settings (it passed the gate, so the
	// switch is on) and may change none of them: a key cannot be the thing
	// that decides what keys may do, or rewrite every chat's instructions.
	for _, method := range []principal.Method{principal.MethodPersonalAccessToken, principal.MethodAgent} {
		gated := principal.Principal{UserID: alice, Method: method}
		if status, _ := as(gated, http.MethodGet, SettingsReadPath, ""); status != http.StatusOK {
			t.Errorf("%s could not read the settings: %d", method, status)
		}
		if status, raw := as(gated, http.MethodPatch, SettingsUpdatePath, `{"api_access_enabled":false}`); status != http.StatusForbidden || !strings.Contains(raw, sessionRequired) {
			t.Errorf("%s changing the settings answered %d %s", method, status, raw)
		}
	}
	var stillOn bool
	if err := pool.QueryRow(t.Context(), `SELECT api_access_enabled FROM "user" WHERE id = $1`, alice).Scan(&stillOn); err != nil || !stillOn {
		t.Errorf("a refused change was written: %v, %v", stillOn, err)
	}

	// The profile: each account's own row, with no credit balance in it.
	if status, raw := as(her, http.MethodPatch, UpdatePath, `{"display_name":"Alice"}`); status != http.StatusOK || !strings.Contains(raw, `"display_name":"Alice"`) {
		t.Fatalf("her profile change answered %d %s", status, raw)
	}
	status, his := as(him, http.MethodGet, ReadPath, "")
	if status != http.StatusOK || !strings.Contains(his, `"id":"`+bob+`"`) || !strings.Contains(his, `"email":"bob@example.test"`) ||
		!strings.Contains(his, `"display_name":null`) || strings.Contains(his, "alice") || strings.Contains(his, "credit") {
		t.Errorf("his profile answered %d %s", status, his)
	}
}
