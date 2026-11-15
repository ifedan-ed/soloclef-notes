package signin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// as sends a request as the account, the way the gate hands it on.
func (h *harness) as(userID string, method principal.Method, verb, path, body string) *httptest.ResponseRecorder {
	h.t.Helper()
	request := httptest.NewRequest(verb, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: userID, Method: method}))
	return h.do(request)
}

func devicesOf(t *testing.T, recorder *httptest.ResponseRecorder) []deviceRead {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("the devices list answered %d %s", recorder.Code, recorder.Body)
	}
	var list []deviceRead
	if err := json.Unmarshal(recorder.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	return list
}

// Each account sees and signs out its own devices and no one else's.
func TestEachAccountListsAndSignsOutOnlyItsOwnDevices(t *testing.T) {
	h, count := newHarness(t)
	herPhone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPhone"))
	h.clock = h.clock.Add(time.Minute)
	herTablet := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPad"))
	hisPhone := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "Bob's Pixel"))
	h.exec(`INSERT INTO push_devices (user_id, token, kind, platform, session_family_id) VALUES
		($1, 'ExponentPushToken[her-phone]', 'expo', 'ios', $2), ($1, 'ExponentPushToken[her-tablet]', 'expo', 'ios', $3), ($4, 'ExponentPushToken[his]', 'expo', 'android', $5)`,
		testpostgres.Alice, *herPhone.SessionID, *herTablet.SessionID, testpostgres.Bob, *hisPhone.SessionID)

	hers := devicesOf(t, h.as(testpostgres.Alice, principal.MethodSession, http.MethodGet, DevicesPath, ""))
	if len(hers) != 2 || hers[0].SessionID != *herTablet.SessionID || hers[1].SessionID != *herPhone.SessionID {
		t.Fatalf("her devices, newest use first: %+v", hers)
	}
	if hers[0].Kind != "app" || hers[0].DeviceLabel == nil || *hers[0].DeviceLabel != "Alice's iPad" ||
		!hers[0].ExpiresAt.After(hers[0].SignedInAt) || hers[0].LastUsedAt.Before(hers[0].SignedInAt) {
		t.Errorf("her newest device reads %+v", hers[0])
	}
	his := devicesOf(t, h.as(testpostgres.Bob, principal.MethodSession, http.MethodGet, DevicesPath, ""))
	if len(his) != 1 || his[0].SessionID != *hisPhone.SessionID {
		t.Fatalf("his devices: %+v", his)
	}
	// A refresh is a use: her phone moves to the top.
	h.clock = h.clock.Add(time.Minute)
	pairOf(t, h.bodyRefresh(RefreshPath, *herPhone.RefreshToken))
	if hers = devicesOf(t, h.as(testpostgres.Alice, principal.MethodSession, http.MethodGet, DevicesPath, "")); hers[0].SessionID != *herPhone.SessionID {
		t.Errorf("after a refresh her phone is not the newest use: %+v", hers)
	}

	// He cannot sign her phone out: the same 404 as an id never made.
	for _, c := range []struct{ why, id string }{
		{"her sign-in, asked by him", *herPhone.SessionID},
		{"an id never made", "33333333-3333-4333-8333-333333333333"},
		{"not an id", "not-an-id"},
	} {
		if got := h.as(testpostgres.Bob, principal.MethodSession, http.MethodDelete, DevicesPath+"/"+c.id, ""); got.Code != http.StatusNotFound {
			t.Errorf("%s: answered %d %s", c.why, got.Code, got.Body)
		}
	}
	if n := count(`SELECT count(*) FROM push_devices WHERE user_id = $1`, testpostgres.Alice); n != 2 {
		t.Errorf("his attempts removed her phones' pushes: %d left", n)
	}
	// She signs her phone out: its sign-in ends and its pushes go; her
	// tablet and his phone keep theirs.
	if got := h.as(testpostgres.Alice, principal.MethodSession, http.MethodDelete, DevicesPath+"/"+*herPhone.SessionID, ""); got.Code != http.StatusNoContent {
		t.Fatalf("signing her phone out answered %d %s", got.Code, got.Body)
	}
	if got := h.bodyRefresh(RefreshPath, *herPhone.RefreshToken); got.Code != http.StatusUnauthorized {
		t.Errorf("her signed-out phone could still refresh: %d", got.Code)
	}
	if n := count(`SELECT count(*) FROM push_devices WHERE token = 'ExponentPushToken[her-phone]'`); n != 0 {
		t.Error("her signed-out phone still takes pushes")
	}
	if got := h.as(testpostgres.Alice, principal.MethodSession, http.MethodDelete, DevicesPath+"/"+*herPhone.SessionID, ""); got.Code != http.StatusNotFound {
		t.Errorf("signing out a device a second time answered %d", got.Code)
	}
	if hers = devicesOf(t, h.as(testpostgres.Alice, principal.MethodSession, http.MethodGet, DevicesPath, "")); len(hers) != 1 || hers[0].SessionID != *herTablet.SessionID {
		t.Errorf("her devices after the sign-out: %+v", hers)
	}

	// Everywhere: every one of her sign-ins ends and every phone of hers
	// stops taking pushes; his are untouched.
	got := h.as(testpostgres.Alice, principal.MethodSession, http.MethodPost, LogoutAllPath, `{"refresh_token":"`+*herTablet.RefreshToken+`"}`)
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), "Successfully logged out from all devices") {
		t.Fatalf("signing out everywhere answered %d %s", got.Code, got.Body)
	}
	if hers = devicesOf(t, h.as(testpostgres.Alice, principal.MethodSession, http.MethodGet, DevicesPath, "")); len(hers) != 0 {
		t.Errorf("she is still signed in on %+v", hers)
	}
	if n := count(`SELECT count(*) FROM push_devices WHERE user_id = $1`, testpostgres.Alice); n != 0 {
		t.Errorf("%d of her phones still take pushes", n)
	}
	if his = devicesOf(t, h.as(testpostgres.Bob, principal.MethodSession, http.MethodGet, DevicesPath, "")); len(his) != 1 {
		t.Errorf("her sign-out everywhere ended his sign-in: %+v", his)
	}
	if n := count(`SELECT count(*) FROM push_devices WHERE user_id = $1`, testpostgres.Bob); n != 1 {
		t.Error("her sign-out everywhere removed his phone's pushes")
	}
	pairOf(t, h.bodyRefresh(RefreshPath, *hisPhone.RefreshToken))
}

// A program acting for the account (an API key, an agent) may not read
// where the person is signed in or sign them out.
func TestTheDevicesRoutesTakeASessionOnly(t *testing.T) {
	h, _ := newHarness(t)
	phone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPhone"))
	for _, c := range []struct{ verb, path string }{
		{http.MethodGet, DevicesPath},
		{http.MethodDelete, DevicesPath + "/" + *phone.SessionID},
		{http.MethodPost, LogoutAllPath},
	} {
		for _, method := range []principal.Method{principal.MethodPersonalAccessToken, principal.MethodAgent} {
			if got := h.as(testpostgres.Alice, method, c.verb, c.path, ""); got.Code != http.StatusForbidden {
				t.Errorf("%s %s as %s: %d %s", c.verb, c.path, method, got.Code, got.Body)
			}
		}
	}
	pairOf(t, h.bodyRefresh(RefreshPath, *phone.RefreshToken))
}

// asSession sends a request as one particular sign-in of the account, the
// way the gate hands on a session token (which always names its sign-in).
func (h *harness) asSession(userID, sessionID, verb, path string) *httptest.ResponseRecorder {
	h.t.Helper()
	request := httptest.NewRequest(verb, path, nil)
	caller := principal.Principal{UserID: userID, Method: principal.MethodSession, SessionID: sessionID}
	return h.do(request.WithContext(principal.WithPrincipal(request.Context(), caller)))
}

// The list marks the sign-in that asked, and only it: the website keeps its
// session in cookies its script cannot read, so this is how it knows which
// row is "This browser" and must not offer to sign itself out by mistake.
func TestTheDevicesListMarksTheSignInThatAsked(t *testing.T) {
	h, _ := newHarness(t)
	phone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPhone"))
	h.clock = h.clock.Add(time.Minute)
	tablet := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPad"))

	for _, asking := range []string{*phone.SessionID, *tablet.SessionID} {
		list := devicesOf(t, h.asSession(testpostgres.Alice, asking, http.MethodGet, DevicesPath))
		if len(list) != 2 {
			t.Fatalf("asked from %s: %+v", asking, list)
		}
		for _, device := range list {
			if device.Current != (device.SessionID == asking) {
				t.Errorf("asked from %s, %s reads current=%v", asking, device.SessionID, device.Current)
			}
		}
	}
	// A session whose sign-in is not one of the rows (none here: the gate
	// never lets such a token in) marks nothing.
	for _, device := range devicesOf(t, h.as(testpostgres.Alice, principal.MethodSession, http.MethodGet, DevicesPath, "")) {
		if device.Current {
			t.Errorf("a caller naming no sign-in was given %s as its own", device.SessionID)
		}
	}
}

// Signing in again in a browser that is already signed in ends the sign-in
// its cookies are replacing: nothing can use it any more, and it must not
// linger in the devices list as an unknown browser. Another browser's
// sign-in, and the phone's, are left alone.
func TestSigningInAgainInTheSameBrowserEndsTheSignInItReplaces(t *testing.T) {
	h, _ := newHarness(t)
	signIn := func(cookie string) *http.Cookie {
		t.Helper()
		form := url.Values{"username": {"alice@example.test"}, "password": {alicePassword}}
		request := httptest.NewRequest(http.MethodPost, PasswordPath, strings.NewReader(form.Encode()))
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != "" {
			request.AddCookie(&http.Cookie{Name: h.routes.Cookies.RefreshName, Value: cookie})
		}
		answer := h.do(request)
		if answer.Code != http.StatusOK {
			t.Fatalf("the sign-in answered %d %s", answer.Code, answer.Body)
		}
		return cookiesOf(answer)[h.routes.Cookies.RefreshName]
	}
	first := signIn("")
	elsewhere := signIn("")
	phone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPhone"))
	again := signIn(first.Value)

	if got := h.do(cookieRefresh(h, first.Value)); got.Code != http.StatusUnauthorized {
		t.Errorf("the replaced sign-in still refreshes: %d", got.Code)
	}
	for why, token := range map[string]string{"the new sign-in": again.Value, "the other browser": elsewhere.Value} {
		if got := h.do(cookieRefresh(h, token)); got.Code != http.StatusOK {
			t.Errorf("%s no longer refreshes: %d %s", why, got.Code, got.Body)
		}
	}
	pairOf(t, h.bodyRefresh(RefreshPath, *phone.RefreshToken))
	if list := devicesOf(t, h.as(testpostgres.Alice, principal.MethodSession, http.MethodGet, DevicesPath, "")); len(list) != 3 {
		t.Errorf("want the new browser, the other browser and the phone: %+v", list)
	}

	// A cookie naming no live sign-in (ended, or never made) is no reason
	// to refuse the sign-in.
	signIn(first.Value)
	signIn("not-a-token-anyone-made")
}

// cookieRefresh is the website's refresh: the refresh token in its cookie.
func cookieRefresh(h *harness, token string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, RefreshPath, nil)
	request.AddCookie(&http.Cookie{Name: h.routes.Cookies.RefreshName, Value: token})
	return request
}
