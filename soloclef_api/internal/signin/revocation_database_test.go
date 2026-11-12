package signin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// These tests ask the real gate (internal/principal's resolver over the real
// directory) about the access tokens this package mints, before and after
// each of the ways a sign-in ends. Until 2026-11-12 every one of those ways
// ended the refresh token and left the access token working for the rest of
// its hour, because the gate verified the token with the secret and read
// only whether the account was active.

// gate is the gate's verdict on a request, with the harness's clock as the
// gate's clock so a test can move past a sign-in's end.
func (h *harness) gate(request *http.Request) (principal.Principal, error) {
	h.t.Helper()
	resolver := principal.Resolver{
		SessionTokens:     principal.SessionToken{Secret: secret},
		Directory:         principal.PostgresDirectory{Pool: h.pool, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))},
		SessionCookieName: h.routes.Cookies.SessionName,
		Now:               func() time.Time { return h.clock },
	}
	return resolver.Resolve(context.Background(), request)
}

// bearer is a request carrying an access token as the phone app sends it.
func bearer(token string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/notebooks", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}

// cookie is a request carrying an access token as the web app's browser does.
func (h *harness) cookie(token string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/notebooks", nil)
	request.AddCookie(&http.Cookie{Name: h.routes.Cookies.SessionName, Value: token})
	return request
}

// accepts fails the test unless the gate lets the request in as the account.
func (h *harness) accepts(why string, request *http.Request, userID string) {
	h.t.Helper()
	caller, err := h.gate(request)
	if err != nil || caller.UserID != userID || caller.Method != principal.MethodSession {
		h.t.Errorf("%s: the gate answered %+v, %v; want %s's session", why, caller, err, userID)
	}
}

// refuses fails the test unless the gate answers the request with the 401.
func (h *harness) refuses(why string, request *http.Request) {
	h.t.Helper()
	if caller, err := h.gate(request); !errors.Is(err, principal.ErrUnauthorized) {
		h.t.Errorf("%s: the gate answered %+v, %v; want it refused", why, caller, err)
	}
}

func TestSigningOutEndsTheAccessTokenAtOnce(t *testing.T) {
	h, _ := newHarness(t)
	web := cookiesOf(h.webSignIn("alice@example.test", alicePassword))
	phone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPhone"))
	h.accepts("the browser, signed in", h.cookie(web["s_session"].Value), testpostgres.Alice)
	h.accepts("the phone, signed in", bearer(phone.AccessToken), testpostgres.Alice)

	// The browser signs out with its refresh cookie.
	request := httptest.NewRequest(http.MethodPost, RevokePath, nil)
	request.AddCookie(web["s_refresh"])
	if got := h.do(request); got.Code != http.StatusOK {
		t.Fatalf("the browser's sign-out answered %d %s", got.Code, got.Body)
	}
	h.refuses("the signed-out browser's access cookie", h.cookie(web["s_session"].Value))
	h.refuses("the signed-out browser's token sent as a bearer", bearer(web["s_session"].Value))
	h.accepts("the phone, which did not sign out", bearer(phone.AccessToken), testpostgres.Alice)

	// The phone signs out with its refresh token in the body.
	if got := h.bodyRefresh(RevokePath, *phone.RefreshToken); got.Code != http.StatusOK {
		t.Fatalf("the phone's sign-out answered %d %s", got.Code, got.Body)
	}
	h.refuses("the signed-out phone's access token", bearer(phone.AccessToken))
}

func TestSigningADeviceOutEndsItsAccessTokensAtOnce(t *testing.T) {
	h, _ := newHarness(t)
	herPhone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPhone"))
	herTablet := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPad"))
	hisPhone := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "Bob's Pixel"))
	// The phone refreshed, and a racing second refresh inside the grace
	// window was handed an access token of its own: three live access tokens
	// for one device, all of which must end with it.
	h.clock = h.clock.Add(time.Minute)
	rotated := pairOf(t, h.bodyRefresh(RefreshPath, *herPhone.RefreshToken))
	raced := pairOf(t, h.bodyRefresh(RefreshPath, *herPhone.RefreshToken))
	if raced.RefreshToken != nil {
		t.Fatalf("the racing refresh was not the grace case: %+v", raced)
	}

	if got := h.as(testpostgres.Alice, principal.MethodSession, http.MethodDelete, DevicesPath+"/"+*herPhone.SessionID, ""); got.Code != http.StatusNoContent {
		t.Fatalf("signing her phone out answered %d %s", got.Code, got.Body)
	}
	for why, token := range map[string]string{
		"the signed-out phone's first token":     herPhone.AccessToken,
		"the signed-out phone's refreshed token": rotated.AccessToken,
		"the grace-window token":                 raced.AccessToken,
	} {
		h.refuses(why, bearer(token))
	}
	h.accepts("her tablet", bearer(herTablet.AccessToken), testpostgres.Alice)
	h.accepts("his phone", bearer(hisPhone.AccessToken), testpostgres.Bob)
}

func TestSigningOutEverywhereEndsEveryAccessTokenOfTheAccount(t *testing.T) {
	h, _ := newHarness(t)
	web := cookiesOf(h.webSignIn("alice@example.test", alicePassword))["s_session"].Value
	phone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPhone"))
	his := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "Bob's Pixel"))

	if got := h.as(testpostgres.Alice, principal.MethodSession, http.MethodPost, LogoutAllPath, ""); got.Code != http.StatusOK {
		t.Fatalf("signing out everywhere answered %d %s", got.Code, got.Body)
	}
	h.refuses("her browser", h.cookie(web))
	h.refuses("her phone", bearer(phone.AccessToken))
	h.accepts("his phone", bearer(his.AccessToken), testpostgres.Bob)

	// Signing in again afterwards is a new sign-in, and it works.
	again := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPhone"))
	h.accepts("her sign-in after it", bearer(again.AccessToken), testpostgres.Alice)
}

func TestANewPasswordEndsEveryAccessTokenOfTheAccount(t *testing.T) {
	h, _ := newHarness(t)

	// By the mailed link.
	phone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPhone"))
	his := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "Bob's Pixel"))
	if got := h.do(jsonPost(ForgotPath, map[string]string{"email": "alice@example.test"})); got.Code != http.StatusAccepted {
		t.Fatalf("the reset mail answered %d", got.Code)
	}
	link := tokenIn(t, h.mail.messages()[0])
	if got := h.do(jsonPost(ResetPath, map[string]string{"token": link, "password": "an entirely new passphrase"})); got.Code != http.StatusOK {
		t.Fatalf("the reset answered %d %s", got.Code, got.Body)
	}
	h.refuses("her phone after the reset", bearer(phone.AccessToken))
	h.accepts("his phone", bearer(his.AccessToken), testpostgres.Bob)

	// By the operator's command (`soloclef-api set-password`), which says
	// every earlier sign-in of the account is ended.
	phone = pairOf(t, h.phoneSignIn("alice@example.test", "an entirely new passphrase", "Alice's iPhone"))
	h.accepts("her phone, signed in with the new password", bearer(phone.AccessToken), testpostgres.Alice)
	if _, _, err := SetPassword(context.Background(), h.pool, NewHasher(1), "alice@example.test", "the operator's passphrase", h.clock); err != nil {
		t.Fatal(err)
	}
	h.refuses("her phone after set-password", bearer(phone.AccessToken))
	h.accepts("his phone", bearer(his.AccessToken), testpostgres.Bob)
}

// A spent refresh token presented after the grace window is a copy, and the
// whole sign-in is ended: the access token the real device holds goes too.
func TestAStolenRefreshTokenEndsTheSignInsAccessTokens(t *testing.T) {
	h, _ := newHarness(t)
	phone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "Alice's iPhone"))
	h.clock = h.clock.Add(time.Minute)
	next := pairOf(t, h.bodyRefresh(RefreshPath, *phone.RefreshToken))
	h.clock = h.clock.Add(rotationGrace + time.Second)
	if got := h.bodyRefresh(RefreshPath, *phone.RefreshToken); got.Code != http.StatusUnauthorized {
		t.Fatalf("the replayed token answered %d", got.Code)
	}
	h.refuses("the device's current access token after the replay", bearer(next.AccessToken))
}

// Rotation does not end a sign-in: the access token minted before a refresh
// keeps working beside the one minted by it, as two tabs of one browser
// hold both for a moment.
func TestARefreshKeepsTheSignInsAccessTokensWorking(t *testing.T) {
	h, _ := newHarness(t)
	web := cookiesOf(h.webSignIn("alice@example.test", alicePassword))
	phone := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "Bob's Pixel"))
	h.clock = h.clock.Add(30 * time.Minute)

	request := httptest.NewRequest(http.MethodPost, RefreshPath, nil)
	request.AddCookie(web["s_refresh"])
	refreshed := cookiesOf(h.do(request))
	next := pairOf(t, h.bodyRefresh(RefreshPath, *phone.RefreshToken))

	h.accepts("the browser's token from before the refresh", h.cookie(web["s_session"].Value), testpostgres.Alice)
	h.accepts("the browser's refreshed token", h.cookie(refreshed["s_session"].Value), testpostgres.Alice)
	h.accepts("the phone's token from before the refresh", bearer(phone.AccessToken), testpostgres.Bob)
	h.accepts("the phone's refreshed token", bearer(next.AccessToken), testpostgres.Bob)
}

// A browser's sign-in has a cap of its own (webAbsolute). An access token
// minted by a refresh just before the cap does not outlive it by its hour.
func TestAnAccessTokenDoesNotOutliveItsSignInsCap(t *testing.T) {
	h, _ := newHarness(t)
	web := cookiesOf(h.webSignIn("alice@example.test", alicePassword))
	refresh := web["s_refresh"]
	// Refreshed weekly (inside the idle time) until a quarter of an hour
	// before the cap.
	start := h.clock
	for h.clock.Before(start.Add(webAbsolute - 8*24*time.Hour)) {
		h.clock = h.clock.Add(7 * 24 * time.Hour)
		request := httptest.NewRequest(http.MethodPost, RefreshPath, nil)
		request.AddCookie(refresh)
		refresh = cookiesOf(h.do(request))["s_refresh"]
		if refresh == nil {
			t.Fatal("a weekly refresh was refused")
		}
	}
	h.clock = start.Add(webAbsolute - 15*time.Minute)
	request := httptest.NewRequest(http.MethodPost, RefreshPath, nil)
	request.AddCookie(refresh)
	last := cookiesOf(h.do(request))["s_session"]
	if last == nil {
		t.Fatal("the last refresh before the cap was refused")
	}
	h.accepts("the token just before the cap", h.cookie(last.Value), testpostgres.Alice)
	h.clock = start.Add(webAbsolute + time.Minute)
	h.refuses("the token after the cap", h.cookie(last.Value))
}
