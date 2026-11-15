package signin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// changePassword posts the password change as one of the account's sign-ins,
// the way the gate hands the request on.
func (h *harness) changePassword(userID, sessionID, current, next string) *httptest.ResponseRecorder {
	h.t.Helper()
	body, _ := json.Marshal(map[string]string{"current_password": current, "new_password": next})
	request := httptest.NewRequest(http.MethodPost, ChangePasswordPath, strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	caller := principal.Principal{UserID: userID, Method: principal.MethodSession, SessionID: sessionID}
	return h.do(request.WithContext(principal.WithPrincipal(request.Context(), caller)))
}

// refusedWith asserts the answer is the refusal named.
func refusedWith(t *testing.T, why string, got *httptest.ResponseRecorder, status int, message string) {
	t.Helper()
	if got.Code != status || !strings.Contains(got.Body.String(), `"message":"`+message+`"`) {
		t.Errorf("%s: answered %d %s, want %d %s", why, got.Code, got.Body, status, message)
	}
}

// live is the gate's own question about a sign-in: whether an access token
// naming it is still let in. Asked of the real directory, so "ended at once"
// means what the gate does, not what a refresh does an hour later.
func (h *harness) live(userID, sessionID string) bool {
	h.t.Helper()
	ok, err := principal.PostgresDirectory{Pool: h.pool}.SessionIsLive(context.Background(), userID, sessionID, h.clock)
	if err != nil {
		h.t.Fatal(err)
	}
	return ok
}

// A phone stays signed in for as long as it is used: every refresh starts
// its 180 days again and nothing caps it from the sign-in. Half a year
// unused ends it, for a refresh and for the gate alike.
func TestAPhoneStaysSignedInWhileUsedAndEndsAfter180DaysUnused(t *testing.T) {
	h, count := newHarness(t)
	used := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "her phone"))
	idle := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "his phone"))

	// 170 days on, both still refresh; hers is refreshed, which starts her
	// 180 days again.
	h.clock = h.clock.Add(170 * 24 * time.Hour)
	token := *pairOf(t, h.bodyRefresh(RefreshPath, *used.RefreshToken)).RefreshToken
	// 181 days after his sign-in, unused all that time: ended.
	h.clock = h.clock.Add(11 * 24 * time.Hour)
	if got := h.bodyRefresh(RefreshPath, *idle.RefreshToken); got.Code != http.StatusUnauthorized {
		t.Errorf("a phone unused for 180 days refreshed: %d", got.Code)
	}
	if h.live(testpostgres.Bob, *idle.SessionID) {
		t.Error("the gate still lets in a phone unused for 180 days")
	}
	// Hers, used again today and then every 170 days, lives on well past any
	// cap the old rules had (a year, then five): ten years of refreshes.
	token = *pairOf(t, h.bodyRefresh(RefreshPath, token)).RefreshToken
	for range 22 {
		h.clock = h.clock.Add(170 * 24 * time.Hour)
		token = *pairOf(t, h.bodyRefresh(RefreshPath, token)).RefreshToken
	}
	if !h.live(testpostgres.Alice, *used.SessionID) {
		t.Error("the gate refused a phone used every 170 days")
	}
	if n := count(`SELECT count(*) FROM refresh_tokens WHERE family_id = $1 AND revoked_at IS NULL
 AND absolute_expiry IS NULL AND expires_at = $2`, *used.SessionID, h.clock.Add(phoneIdle)); n != 1 {
		t.Errorf("%d live tokens with no cap and 180 days to run", n)
	}
	// Then 180 days and a minute unused: ended.
	h.clock = h.clock.Add(phoneIdle + time.Minute)
	if got := h.bodyRefresh(RefreshPath, token); got.Code != http.StatusUnauthorized {
		t.Errorf("a phone refreshed after 180 days unused: %d", got.Code)
	}
}

// A wrong current password changes nothing, signs nobody out, and is written
// to the account's activity log.
func TestAWrongCurrentPasswordIsRefused(t *testing.T) {
	h, count := newHarness(t)
	phone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "her phone"))
	other := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "her tablet"))

	refusedWith(t, "a wrong current password", h.changePassword(testpostgres.Alice, *phone.SessionID, "not her password", "a brand new passphrase"),
		http.StatusBadRequest, changeBadCurrent)
	refusedWith(t, "no current password", h.changePassword(testpostgres.Alice, *phone.SessionID, "", "a brand new passphrase"),
		http.StatusBadRequest, changeBadCurrent)
	refusedWith(t, "another account's password", h.changePassword(testpostgres.Alice, *phone.SessionID, bobPassword, "a brand new passphrase"),
		http.StatusBadRequest, changeBadCurrent)

	if got := h.phoneSignIn("alice@example.test", alicePassword, "check"); got.Code != http.StatusOK {
		t.Errorf("her old password stopped working after refused changes: %d", got.Code)
	}
	if !h.live(testpostgres.Alice, *other.SessionID) {
		t.Error("a refused change signed her tablet out")
	}
	if n := count(`SELECT count(*) FROM logs WHERE user_id = $1 AND level = 'WARNING' AND status = 'FAILED'
 AND source = 'Account' AND log_metadata->>'event' = 'password_change_refused'`, testpostgres.Alice); n != 2 {
		t.Errorf("%d refused changes in her activity log, want the 2 wrong passwords (an empty one is not a guess)", n)
	}
	if n := count(`SELECT count(*) FROM logs WHERE user_id = $1`, testpostgres.Bob); n != 0 {
		t.Error("her refused changes were logged to his account")
	}
}

// The change ends every other sign-in of the account at once -- the gate
// refuses their access tokens on the next request, their refresh tokens are
// dead, their phones stop getting pushes -- and the sign-in that asked keeps
// working. Another account is untouched.
func TestAPasswordChangeEndsEveryOtherSignInAndKeepsThisOne(t *testing.T) {
	h, count := newHarness(t)
	phone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "her phone"))
	tablet := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "her tablet"))
	browser := cookiesOf(h.webSignIn("alice@example.test", alicePassword))["s_refresh"].Value
	browserFamily := ""
	if err := h.pool.QueryRow(context.Background(), `SELECT family_id::text FROM refresh_tokens WHERE token_hash = $1`, hashToken(browser)).Scan(&browserFamily); err != nil {
		t.Fatal(err)
	}
	his := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "his phone"))
	h.exec(`INSERT INTO push_devices (user_id, token, kind, platform, session_family_id) VALUES
 ($1, 'ExponentPushToken[phone]', 'expo', 'ios', $2), ($1, 'ExponentPushToken[tablet]', 'expo', 'ios', $3),
 ($1, 'ExponentPushToken[unbound]', 'expo', 'android', NULL), ($4, 'ExponentPushToken[his]', 'expo', 'android', $5)`,
		testpostgres.Alice, *phone.SessionID, *tablet.SessionID, testpostgres.Bob, *his.SessionID)

	h.clock = h.clock.Add(time.Minute)
	got := h.changePassword(testpostgres.Alice, *phone.SessionID, alicePassword, "a brand new passphrase")
	if got.Code != http.StatusOK {
		t.Fatalf("the change answered %d %s", got.Code, got.Body)
	}
	var answer passwordChanged
	if err := json.Unmarshal(got.Body.Bytes(), &answer); err != nil || answer.SignedOutSessions != 2 {
		t.Errorf("the answer %s says %d sign-ins were ended, want 2", got.Body, answer.SignedOutSessions)
	}

	// At once: the gate's own check, before any refresh.
	if h.live(testpostgres.Alice, *tablet.SessionID) || h.live(testpostgres.Alice, browserFamily) {
		t.Error("another of her sign-ins is still let in by the gate")
	}
	if !h.live(testpostgres.Alice, *phone.SessionID) || !h.live(testpostgres.Bob, *his.SessionID) {
		t.Fatal("the change ended the sign-in that asked, or his")
	}
	if code := h.bodyRefresh(RefreshPath, *tablet.RefreshToken).Code; code != http.StatusUnauthorized {
		t.Errorf("her tablet refreshed after the change: %d", code)
	}
	if code := h.bodyRefresh(RefreshPath, browser).Code; code != http.StatusUnauthorized {
		t.Errorf("her browser refreshed after the change: %d", code)
	}
	// The phone that asked goes on as before.
	pairOf(t, h.bodyRefresh(RefreshPath, *phone.RefreshToken))
	pairOf(t, h.bodyRefresh(RefreshPath, *his.RefreshToken))

	if n := count(`SELECT count(*) FROM push_devices WHERE user_id = $1 AND session_family_id = $2`, testpostgres.Alice, *phone.SessionID); n != 1 {
		t.Error("the phone that asked lost its pushes")
	}
	if n := count(`SELECT count(*) FROM push_devices WHERE user_id = $1`, testpostgres.Alice); n != 1 {
		t.Errorf("%d of her phones still get pushes, want only the one that asked", n)
	}
	if n := count(`SELECT count(*) FROM push_devices WHERE user_id = $1`, testpostgres.Bob); n != 1 {
		t.Error("her change took his phone's pushes")
	}

	if code := h.phoneSignIn("alice@example.test", alicePassword, "x").Code; code != http.StatusBadRequest {
		t.Errorf("her old password still signs in: %d", code)
	}
	pairOf(t, h.phoneSignIn("alice@example.test", "a brand new passphrase", "x"))
	if n := count(`SELECT count(*) FROM logs WHERE user_id = $1 AND level = 'INFO' AND status = 'SUCCESS' AND source = 'Account'
 AND message = 'Password changed. 2 other sign-ins were signed out.' AND log_metadata->>'kept_session_id' = $2`,
		testpostgres.Alice, *phone.SessionID); n != 1 {
		t.Errorf("%d changes in her activity log", n)
	}
}

// The new password is held to the sign-up's rules, and a refused one changes
// nothing; only a person's session may change it, never an API key.
func TestANewPasswordFollowsTheRulesAndOnlyASessionMayChangeIt(t *testing.T) {
	h, _ := newHarness(t)
	phone := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "her phone"))
	tablet := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "her tablet"))
	for _, c := range []struct{ why, password string }{
		{"too short", "short"},
		{"the address itself", "Alice@Example.test"},
		{"too long", strings.Repeat("x", maxPasswordLength+1)},
	} {
		refusedWith(t, c.why, h.changePassword(testpostgres.Alice, *phone.SessionID, alicePassword, c.password), http.StatusBadRequest, changeInvalidPassword)
	}
	key := httptest.NewRequest(http.MethodPost, ChangePasswordPath, strings.NewReader(`{"current_password":"`+alicePassword+`","new_password":"a brand new passphrase"}`))
	key = key.WithContext(principal.WithPrincipal(key.Context(), principal.Principal{UserID: testpostgres.Alice, Method: principal.MethodPersonalAccessToken}))
	refusedWith(t, "an API key", h.do(key), http.StatusForbidden, sessionRequired)

	if !h.live(testpostgres.Alice, *tablet.SessionID) {
		t.Error("a refused change signed her tablet out")
	}
	pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "check"))
}

// An account single sign-on made has no password to give. It may set one
// only from a sign-in the provider proved in the last ten minutes -- the
// sign-in itself, kept across a refresh, or the app's confirmation of an
// older one -- and never from a sign-in nothing proved. An account that has a
// password still needs it, however freshly the provider proved the sign-in.
func TestAnAccountSingleSignOnMadeSetsAPasswordOnlyFromAFreshProof(t *testing.T) {
	h, count := newHarness(t)
	const carol = "33333333-3333-4333-8333-333333333333"
	h.exec(`INSERT INTO "user" (id, email, hashed_password, is_verified) VALUES ($1, 'carol@example.test', NULL, true)`, carol)
	label := "Carol's phone"
	ctx := context.Background()
	start := func(userID string, proof Proof) TokenPair {
		t.Helper()
		pair, err := h.routes.StartPhoneSession(ctx, userID, &label, proof)
		if err != nil {
			t.Fatal(err)
		}
		return pair
	}

	// The app asks first which form to show: she has no password, Alice has one.
	for _, c := range []struct {
		userID string
		want   string
	}{{carol, `{"has_password":false}`}, {testpostgres.Alice, `{"has_password":true}`}} {
		request := httptest.NewRequest(http.MethodGet, PasswordStatePath, nil)
		request = request.WithContext(principal.WithPrincipal(request.Context(), principal.Principal{UserID: c.userID, Method: principal.MethodSession, SessionID: "x"}))
		if got := h.do(request); got.Code != http.StatusOK || strings.TrimSpace(got.Body.String()) != c.want {
			t.Errorf("the password state of %s answered %d %s", c.userID, got.Code, got.Body)
		}
	}

	// A sign-in nothing proved through the provider (here, the one the
	// end-to-end suite mints): refused, with the code the app answers by
	// confirming.
	unproved := start(carol, ProvedByPassword)
	refusedWith(t, "a sign-in the provider never proved", h.changePassword(carol, *unproved.SessionID, "", "carol's first password"),
		http.StatusForbidden, changeConfirmSSO)

	// A sign-in through the provider eleven minutes ago: too old.
	stale := start(carol, ProvedBySSO)
	h.clock = h.clock.Add(11 * time.Minute)
	refusedWith(t, "a proof eleven minutes old", h.changePassword(carol, *stale.SessionID, "", "carol's first password"),
		http.StatusForbidden, changeConfirmSSO)
	// The app confirms that same sign-in through the provider: now it may.
	if stamped, err := h.routes.ConfirmSessionBySSO(ctx, carol, *stale.SessionID); err != nil || !stamped {
		t.Fatalf("confirming answered %v, %v", stamped, err)
	}
	// The proof survives a refresh: the successor carries it.
	h.clock = h.clock.Add(3 * time.Minute)
	pairOf(t, h.bodyRefresh(RefreshPath, *stale.RefreshToken))
	if got := h.changePassword(carol, *stale.SessionID, "", "carol's first password"); got.Code != http.StatusOK {
		t.Fatalf("a fresh proof answered %d %s", got.Code, got.Body)
	}
	if !h.live(carol, *stale.SessionID) || h.live(carol, *unproved.SessionID) {
		t.Error("setting her first password did not keep only the sign-in that asked")
	}
	pairOf(t, h.phoneSignIn("carol@example.test", "carol's first password", "check"))
	if n := count(`SELECT count(*) FROM logs WHERE user_id = $1 AND message LIKE 'Password set.%' AND (log_metadata->>'first_password')::boolean`, carol); n != 1 {
		t.Errorf("%d first passwords in her activity log", n)
	}

	// Now she has a password, a fresh proof is no longer enough: it is asked.
	fresh := start(carol, ProvedBySSO)
	refusedWith(t, "an account with a password, freshly proved, without it", h.changePassword(carol, *fresh.SessionID, "", "another passphrase"),
		http.StatusBadRequest, changeBadCurrent)
	// And Alice, who has had a password all along, is held to it too.
	hers := start(testpostgres.Alice, ProvedBySSO)
	refusedWith(t, "her account, freshly proved, without her password", h.changePassword(testpostgres.Alice, *hers.SessionID, "", "another passphrase"),
		http.StatusBadRequest, changeBadCurrent)
	// A confirmation names the account beside the sign-in: Carol's proof
	// stamps none of Alice's.
	if stamped, _ := h.routes.ConfirmSessionBySSO(ctx, carol, *hers.SessionID); stamped {
		t.Error("one account's confirmation stamped another's sign-in")
	}
}
