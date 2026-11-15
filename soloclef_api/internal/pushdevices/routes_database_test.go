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
	text  func(sql string, args ...any) string
	seed  func(statements string)
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
	}, text: func(sql string, args ...any) string {
		t.Helper()
		var value string
		if err := pool.QueryRow(context.Background(), sql, args...).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}, seed: func(statements string) {
		t.Helper()
		testpostgres.Exec(t, pool, statements)
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
	if got := h.as(alice, principal.MethodSession, http.MethodPut, PreferencesPath, `{"choices":{"source":false}}`); got.Code != http.StatusOK {
		t.Fatalf("saving answered %d %s", got.Code, got.Body)
	}
	h.as(alice, principal.MethodSession, http.MethodPut, PreferencesPath, `{"choices":{"output":false}}`)
	hers := read(alice)
	if hers["source"] || hers["output"] || !hers["automation"] {
		t.Errorf("her choices: %v", hers)
	}
	for kind, on := range read(bob) {
		if !on {
			t.Errorf("her choices turned off his %s", kind)
		}
	}
	// "answer" was offered once and nothing ever sent it: it is refused now.
	for _, body := range []string{`{"choices":{"mention":false}}`, `{"choices":{"answer":false}}`, `{"choices":{"output":"no"}}`, `{}`} {
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
		{http.MethodPut, PreferencesPath, `{"choices":{"output":false}}`},
	} {
		if got := h.as(testpostgres.Alice, principal.MethodPersonalAccessToken, c.verb, c.target, c.body); got.Code != http.StatusForbidden {
			t.Errorf("%s %s with an API key: %d %s", c.verb, c.target, got.Code, got.Body)
		}
	}
	if n := h.count(`SELECT count(*) FROM push_devices`); n != 0 {
		t.Error("an API key registered a phone")
	}
}

// Quiet hours are saved and read back on the person's own clock and zone,
// left alone by a change to the switches, switched off by null, and refused
// when they cannot mean one thing.
func TestQuietHoursAreSavedInThePersonsZone(t *testing.T) {
	h := newHarness(t)
	alice := testpostgres.Alice
	put := func(body string) (int, preferencesOut) {
		t.Helper()
		got := h.as(alice, principal.MethodSession, http.MethodPut, PreferencesPath, body)
		var out preferencesOut
		_ = json.Unmarshal(got.Body.Bytes(), &out)
		return got.Code, out
	}
	code, out := put(`{"quiet_hours":{"start":"22:00","end":"07:00","time_zone":"Africa/Lagos"}}`)
	if code != http.StatusOK || out.QuietHours == nil || *out.QuietHours != (quietHours{"22:00", "07:00", "Africa/Lagos"}) {
		t.Fatalf("saving answered %d %+v", code, out.QuietHours)
	}
	if n := h.count(`SELECT count(*) FROM push_preferences WHERE user_id = $1 AND quiet_start_minute = 1320 AND quiet_end_minute = 420`, alice); n != 1 {
		t.Error("the span is not stored as minutes")
	}
	// A switch changed alone keeps the span.
	if code, out = put(`{"choices":{"mail":false}}`); code != http.StatusOK || out.QuietHours == nil || out.Choices["mail"] {
		t.Errorf("a switch change: %d %+v", code, out)
	}
	for _, bad := range []string{
		`{"quiet_hours":{"start":"7:00","end":"08:00","time_zone":"UTC"}}`,
		`{"quiet_hours":{"start":"08:00","end":"08:00","time_zone":"UTC"}}`,
		`{"quiet_hours":{"start":"22:00","end":"07:00","time_zone":"Local"}}`,
		`{"quiet_hours":{"start":"22:00","end":"07:00","time_zone":"Mars/Olympus"}}`,
		`{"quiet_hours":"tonight"}`,
	} {
		if code, _ := put(bad); code != http.StatusUnprocessableEntity {
			t.Errorf("%s answered %d", bad, code)
		}
	}
	if code, out = put(`{"quiet_hours":null}`); code != http.StatusOK || out.QuietHours != nil || out.Choices["mail"] {
		t.Errorf("switching off: %d %+v", code, out)
	}
	got := h.as(alice, principal.MethodSession, http.MethodGet, PreferencesPath, "")
	if !strings.Contains(got.Body.String(), `"quiet_hours":null`) {
		t.Errorf("off reads as %s", got.Body)
	}
}

// appleRegistration is an iPhone's registration of its APNs token.
func appleRegistration(token, environment, session string) string {
	body, _ := json.Marshal(map[string]any{"token": token, "kind": "apns", "environment": environment, "platform": "ios", "session_id": session})
	return string(body)
}

// An iPhone moving to APNs registers its Apple token under its sign-in, and
// the Expo token that sign-in held goes in the same statement, so the phone
// is never pushed twice. Each kind's token is checked for its own shape.
func TestAnIPhoneRegistersItsAppleTokenInPlaceOfItsExpoOne(t *testing.T) {
	h := newHarness(t)
	alice, bob := testpostgres.Alice, testpostgres.Bob
	const tabletSession = "aaaaaaaa-0000-4000-8000-000000000004"
	h.seed(`INSERT INTO refresh_tokens (user_id, token_hash, expires_at, family_id, kind) VALUES
 ('` + alice + `', 'h4', '2027-01-01', '` + tabletSession + `', 'app')`)
	apple := strings.Repeat("A1b2", 16)

	// Before: her phone on Expo (an app build from before APNs names no
	// kind), her tablet on Expo, his phone on Expo.
	h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, registration(herToken, herSession))
	h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, registration("ExponentPushToken[her-tablet]", tabletSession))
	h.as(bob, principal.MethodSession, http.MethodPost, DevicesPath, registration("ExponentPushToken[his]", hisSession))
	if kind := h.text(`SELECT kind || ' ' || coalesce(apns_environment, '-') FROM push_devices WHERE token = $1`, herToken); kind != "expo -" {
		t.Fatalf("a registration with no kind is %q", kind)
	}

	got := h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, appleRegistration(apple, "production", herSession))
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"kind":"apns"`) || !strings.Contains(got.Body.String(), strings.ToLower(apple)) {
		t.Fatalf("her APNs registration answered %d %s", got.Code, got.Body)
	}
	if row := h.text(`SELECT token || ' ' || kind || ' ' || apns_environment FROM push_devices WHERE session_family_id = $1`, herSession); row != strings.ToLower(apple)+" apns production" {
		t.Errorf("her phone's sign-in holds %q", row)
	}
	if n := h.count(`SELECT count(*) FROM push_devices WHERE token = $1`, herToken); n != 0 {
		t.Error("her phone's Expo token is still registered beside its Apple one")
	}
	// Her tablet's sign-in and his phone are not her phone's.
	if n := h.count(`SELECT count(*) FROM push_devices WHERE token IN ('ExponentPushToken[her-tablet]', 'ExponentPushToken[his]')`); n != 2 {
		t.Error("another sign-in's token was removed")
	}
	// A sandbox token (a development build) is taken too, and replaces the
	// production one for the same sign-in.
	sandbox := strings.Repeat("cd", 32)
	if got := h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, appleRegistration(sandbox, "sandbox", herSession)); got.Code != http.StatusOK {
		t.Fatalf("a sandbox registration answered %d %s", got.Code, got.Body)
	}
	if row := h.text(`SELECT token || ' ' || apns_environment FROM push_devices WHERE session_family_id = $1`, herSession); row != sandbox+" sandbox" {
		t.Errorf("her phone's sign-in holds %q", row)
	}

	expoWithEnvironment, _ := json.Marshal(map[string]any{"token": "ExponentPushToken[x]", "kind": "expo", "environment": "production", "platform": "ios", "session_id": herSession})
	appleAsExpo, _ := json.Marshal(map[string]any{"token": sandbox, "kind": "expo", "platform": "ios", "session_id": herSession})
	otherKind, _ := json.Marshal(map[string]any{"token": sandbox, "kind": "fcm", "platform": "android", "session_id": herSession})
	for _, c := range []struct{ why, body, says string }{
		{"an APNs token that is not hex", appleRegistration("ExponentPushToken[x]", "production", herSession), "APNs device token"},
		{"an APNs token too short", appleRegistration("abcd", "production", herSession), "APNs device token"},
		{"an APNs token of odd length", appleRegistration(strings.Repeat("a", 65), "production", herSession), "APNs device token"},
		{"an APNs token from Android", strings.Replace(appleRegistration(sandbox, "production", herSession), `"ios"`, `"android"`, 1), "platform"},
		{"an APNs token with no environment", appleRegistration(sandbox, "", herSession), "environment"},
		{"an environment Apple has not", appleRegistration(sandbox, "development", herSession), "environment"},
		{"an Expo token with an environment", string(expoWithEnvironment), "environment"},
		{"an Apple token named Expo's", string(appleAsExpo), "Expo push token"},
		{"a kind there is not", string(otherKind), "kind"},
	} {
		if got := h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, c.body); got.Code != http.StatusUnprocessableEntity || !strings.Contains(got.Body.String(), c.says) {
			t.Errorf("%s: %d %s", c.why, got.Code, got.Body)
		}
	}
	if row := h.text(`SELECT token FROM push_devices WHERE session_family_id = $1`, herSession); row != sandbox {
		t.Errorf("a refused registration changed her phone's token to %q", row)
	}

	// Signing out forgets the Apple token, however its hex is written.
	if got := h.as(alice, principal.MethodSession, http.MethodDelete, DevicesPath, `{"token":"`+strings.ToUpper(sandbox)+`"}`); got.Code != http.StatusNoContent {
		t.Fatalf("her removal answered %d", got.Code)
	}
	if n := h.count(`SELECT count(*) FROM push_devices WHERE token = $1`, sandbox); n != 0 {
		t.Error("her Apple token is still registered after she forgot it")
	}
}

// versioned is a registration that says the app's version (nil leaves the
// field out, as a build from before it does).
func versioned(token, session string, version any) string {
	fields := map[string]any{"token": token, "platform": "ios", "session_id": session}
	if version != nil {
		fields["app_version"] = version
	}
	body, _ := json.Marshal(fields)
	return string(body)
}

// A phone's app version is kept as it was last sent, so a phone that
// updated reads as current on its next start; a build that sends none is
// kept as none; a version that is not a plain dotted number is refused with
// the field named. The App Store version a phone was told about stays with
// the phone when its token is replaced, so a new token is not told the
// same version again.
func TestAPhoneSaysWhichVersionOfTheAppItRuns(t *testing.T) {
	h := newHarness(t)
	alice := testpostgres.Alice
	version := func() string {
		return h.text(`SELECT coalesce(app_version, '-') FROM push_devices WHERE session_family_id = $1`, herSession)
	}

	got := h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, versioned(herToken, herSession, "1.1.0"))
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"app_version":"1.1.0"`) {
		t.Fatalf("answered %d %s", got.Code, got.Body)
	}
	h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, versioned(herToken, herSession, "1.2.0"))
	if v := version(); v != "1.2.0" {
		t.Errorf("after updating, the phone reads as %q", v)
	}
	h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, versioned(herToken, herSession, nil))
	if v := version(); v != "-" {
		t.Errorf("a build that says no version is stored as %q", v)
	}

	for _, bad := range []any{"1.2.0-beta", "", "v1.2", "1..2", 12} {
		got := h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, versioned(herToken, herSession, bad))
		want := "app_version"
		if _, isNumber := bad.(int); isNumber {
			// Not a string at all: the body does not read as a registration.
			want = "push registration"
		}
		if got.Code != http.StatusUnprocessableEntity || !strings.Contains(got.Body.String(), want) {
			t.Errorf("%v: %d %s", bad, got.Code, got.Body)
		}
	}

	h.seed(`UPDATE push_devices SET update_notified_version = '1.3.0' WHERE token = '` + herToken + `'`)
	apple := strings.Repeat("ab", 32)
	h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, appleRegistration(apple, "production", herSession))
	if told := h.text(`SELECT coalesce(update_notified_version, '-') FROM push_devices WHERE token = $1`, apple); told != "1.3.0" {
		t.Errorf("the replaced token's told version was lost: %q", told)
	}
}

// A phone says which bundle it runs: the update's id (lower-cased, so one
// update is one value however it is written) and the native runtime, kept as
// last sent; a bundle that sends neither is stored with neither, and a value
// of the wrong shape is refused with the field named.
func TestAPhoneSaysWhichBundleItRuns(t *testing.T) {
	h := newHarness(t)
	alice := testpostgres.Alice
	bundle := func() string {
		return h.text(`SELECT coalesce(update_id::text, '-') || ' ' || coalesce(runtime_version, '-') FROM push_devices WHERE session_family_id = $1`, herSession)
	}
	register := func(fields map[string]any) *httptest.ResponseRecorder {
		body := map[string]any{"token": herToken, "platform": "ios", "session_id": herSession, "app_version": "1.1.0"}
		for name, value := range fields {
			body[name] = value
		}
		encoded, _ := json.Marshal(body)
		return h.as(alice, principal.MethodSession, http.MethodPost, DevicesPath, string(encoded))
	}

	const update = "0193A3C4-5E6F-7A8B-9C0D-1E2F3A4B5C6D"
	got := register(map[string]any{"update_id": update, "runtime_version": "c318ec88645f808a398943c00fd6585aa5d7a690"})
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"update_id":"0193a3c4-5e6f-7a8b-9c0d-1e2f3a4b5c6d"`) ||
		!strings.Contains(got.Body.String(), `"runtime_version":"c318ec88645f808a398943c00fd6585aa5d7a690"`) {
		t.Fatalf("answered %d %s", got.Code, got.Body)
	}
	if b := bundle(); b != "0193a3c4-5e6f-7a8b-9c0d-1e2f3a4b5c6d c318ec88645f808a398943c00fd6585aa5d7a690" {
		t.Errorf("stored %q", b)
	}
	// The bundle built into the app has no update id; an older bundle sends
	// neither field.
	register(map[string]any{"update_id": nil, "runtime_version": "1.1.0"})
	if b := bundle(); b != "- 1.1.0" {
		t.Errorf("the built-in bundle is stored as %q", b)
	}
	register(nil)
	if b := bundle(); b != "- -" {
		t.Errorf("a bundle that says neither is stored as %q", b)
	}
	for field, bad := range map[string]any{"update_id": "not-a-uuid", "runtime_version": "has space"} {
		if got := register(map[string]any{field: bad}); got.Code != http.StatusUnprocessableEntity || !strings.Contains(got.Body.String(), field) {
			t.Errorf("%s=%v: %d %s", field, bad, got.Code, got.Body)
		}
	}
}
