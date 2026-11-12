package pushdevices

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// databaseVariable names a throwaway Postgres. **Every account in it is
// deleted** (internal/testpostgres).
const databaseVariable = "SOLOCLEF_API_TEST_PUSHDEVICES_DATABASE"

const (
	herSession   = "aaaaaaaa-0000-4000-8000-000000000001"
	hisSession   = "bbbbbbbb-0000-4000-8000-000000000002"
	endedSession = "aaaaaaaa-0000-4000-8000-000000000003"
	herToken     = "ExponentPushToken[her-phone]"
)

type harness struct {
	t     *testing.T
	api   huma.API
	count func(sql string, args ...any) int
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	pool := testpostgres.Open(t, databaseVariable)
	clock := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	// One live sign-in each, and one of hers that has ended.
	testpostgres.Exec(t, pool, `
INSERT INTO refresh_tokens (user_id, token_hash, expires_at, family_id, kind) VALUES
 ('`+testpostgres.Alice+`', 'h1', '2027-01-01', '`+herSession+`', 'app'),
 ('`+testpostgres.Bob+`',   'h2', '2027-01-01', '`+hisSession+`', 'app'),
 ('`+testpostgres.Alice+`', 'h3', '2026-11-01', '`+endedSession+`', 'app');`)
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	Routes{Database: pool, Errors: errorenvelope.Writer{}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now: func() time.Time { return clock }}.Register(api)
	return &harness{t: t, api: api, count: func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}}
}

func (h *harness) as(userID string, method principal.Method, verb, target, body string) *httptest.ResponseRecorder {
	h.t.Helper()
	request := httptest.NewRequest(verb, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: userID, Method: method}))
	recorder := httptest.NewRecorder()
	h.api.Adapter().ServeHTTP(recorder, request)
	return recorder
}

func registration(token, session string) string {
	body, _ := json.Marshal(map[string]any{"token": token, "platform": "ios", "label": "  Alice's   iPhone ", "language": " en ", "session_id": session})
	return string(body)
}

// A phone is registered to its own account under a live sign-in, and only
// that account can forget it.
func TestAPhoneIsRegisteredAndForgottenByItsOwnAccountAlone(t *testing.T) {
	h := newHarness(t)
	alice, bob := testpostgres.Alice, testpostgres.Bob

	got := h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, registration(herToken, herSession))
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"label":"Alice's iPhone"`) || !strings.Contains(got.Body.String(), `"language":"en"`) {
		t.Fatalf("her registration answered %d %s", got.Code, got.Body)
	}
	if n := h.count(`SELECT count(*) FROM push_devices WHERE user_id = $1 AND token = $2 AND session_family_id = $3`, alice, herToken, herSession); n != 1 {
		t.Fatal("her phone is not registered under her sign-in")
	}
	// Registering again is the same row, refreshed.
	h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, registration(herToken, herSession))
	if n := h.count(`SELECT count(*) FROM push_devices`); n != 1 {
		t.Errorf("%d rows after registering twice", n)
	}

	for _, c := range []struct {
		why, caller, body string
		status            int
		says              string
	}{
		{"her sign-in that has ended", alice, registration("ExponentPushToken[other]", endedSession), 401, "PUSH_SESSION_ENDED"},
		{"his sign-in, named by her", alice, registration("ExponentPushToken[other]", hisSession), 401, "PUSH_SESSION_ENDED"},
		{"a token that is not Expo's", alice, registration("not-a-token", herSession), 422, "Expo push token"},
		{"no session id", alice, registration("ExponentPushToken[other]", ""), 422, "UUID"},
		{"another platform", alice, strings.Replace(registration("ExponentPushToken[other]", herSession), `"ios"`, `"web"`, 1), 422, "platform"},
	} {
		got := h.as(c.caller, principal.MethodSession, http.MethodPost, DevicesPath, c.body)
		if got.Code != c.status || !strings.Contains(got.Body.String(), c.says) {
			t.Errorf("%s: %d %s", c.why, got.Code, got.Body)
		}
	}
	if n := h.count(`SELECT count(*) FROM push_devices`); n != 1 {
		t.Errorf("refused registrations left %d rows", n)
	}

	// He cannot forget her phone, by body or by query; it is the same 204.
	if got := h.as(bob, principal.MethodSession, http.MethodDelete, DevicesPath, `{"token":"`+herToken+`"}`); got.Code != http.StatusNoContent {
		t.Errorf("his removal answered %d", got.Code)
	}
	h.as(bob, principal.MethodSession, http.MethodDelete, DevicesPath+"?token="+herToken, "")
	if n := h.count(`SELECT count(*) FROM push_devices WHERE user_id = $1`, alice); n != 1 {
		t.Fatal("his requests forgot her phone")
	}
	// She can, with the token in the query as the app sends it.
	if got := h.as(alice, principal.MethodSession, http.MethodDelete, DevicesPath+"?token="+herToken, ""); got.Code != http.StatusNoContent {
		t.Fatalf("her removal answered %d %s", got.Code, got.Body)
	}
	if n := h.count(`SELECT count(*) FROM push_devices`); n != 0 {
		t.Error("her phone is still registered")
	}

	// The phone signed in as him next: its token moves to him, under his
	// sign-in.
	h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, registration(herToken, herSession))
	if got := h.as(bob, principal.MethodSession, http.MethodPost, DevicesPath, registration(herToken, hisSession)); got.Code != http.StatusOK {
		t.Fatalf("his registration of the phone answered %d %s", got.Code, got.Body)
	}
	if n := h.count(`SELECT count(*) FROM push_devices WHERE user_id = $1 AND token = $2 AND session_family_id = $3`, bob, herToken, hisSession); n != 1 {
		t.Error("the phone did not move to the account signed in on it now")
	}
}

// Each account's choices are its own; a kind not named keeps its choice.
func TestEachAccountKeepsItsOwnChoices(t *testing.T) {
	h := newHarness(t)
	alice, bob := testpostgres.Alice, testpostgres.Bob
	read := func(userID string) map[string]bool {
		t.Helper()
		got := h.as(userID, principal.MethodSession, http.MethodGet, PreferencesPath, "")
		var body preferencesOut
		if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &body) != nil || len(body.Kinds) != len(Kinds) {
			t.Fatalf("reading choices answered %d %s", got.Code, got.Body)
		}
		return body.Choices
	}
	for kind, on := range read(alice) {
		if !on {
			t.Errorf("%s is off before anything was chosen", kind)
		}
	}
	if got := h.as(alice, principal.MethodSession, http.MethodPut, PreferencesPath, `{"choices":{"answer":false}}`); got.Code != http.StatusOK {
		t.Fatalf("saving answered %d %s", got.Code, got.Body)
	}
	h.as(alice, principal.MethodSession, http.MethodPut, PreferencesPath, `{"choices":{"output":false}}`)
	hers := read(alice)
	if hers["answer"] || hers["output"] || !hers["automation"] {
		t.Errorf("her choices: %v", hers)
	}
	for kind, on := range read(bob) {
		if !on {
			t.Errorf("her choices turned off his %s", kind)
		}
	}
	for _, body := range []string{`{"choices":{"mention":false}}`, `{"choices":{"answer":"no"}}`, `{}`} {
		if got := h.as(alice, principal.MethodSession, http.MethodPut, PreferencesPath, body); got.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s answered %d %s", body, got.Code, got.Body)
		}
	}
}

// A program acting for the account may not register a phone or change
// what it is sent.
func TestThePushRoutesTakeASessionOnly(t *testing.T) {
	h := newHarness(t)
	for _, c := range []struct{ verb, target, body string }{
		{http.MethodPost, DevicesPath, registration(herToken, herSession)},
		{http.MethodDelete, DevicesPath + "?token=" + herToken, ""},
		{http.MethodGet, PreferencesPath, ""},
		{http.MethodPut, PreferencesPath, `{"choices":{"answer":false}}`},
	} {
		if got := h.as(testpostgres.Alice, principal.MethodPersonalAccessToken, c.verb, c.target, c.body); got.Code != http.StatusForbidden {
			t.Errorf("%s %s with an API key: %d %s", c.verb, c.target, got.Code, got.Body)
		}
	}
	if n := h.count(`SELECT count(*) FROM push_devices`); n != 0 {
		t.Error("an API key registered a phone")
	}
}
