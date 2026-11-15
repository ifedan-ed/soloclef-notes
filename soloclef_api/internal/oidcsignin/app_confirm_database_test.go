package oidcsignin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/signin"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// confirm posts the app's confirmation as the caller the gate would hand on.
func (h *harness) confirm(caller principal.Principal, code, verifier string) *httptest.ResponseRecorder {
	h.t.Helper()
	body, _ := json.Marshal(map[string]string{"code": code, "verifier": verifier})
	request := httptest.NewRequest(http.MethodPost, AppConfirmPath, strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	request = request.WithContext(principal.WithPrincipal(request.Context(), caller))
	recorder := httptest.NewRecorder()
	h.api.Adapter().ServeHTTP(recorder, request)
	return recorder
}

// The app confirms its own sign-in through the provider: only with its own
// verifier, only for its own account, once per code, and only a session's
// sign-in is stamped -- never another of the account's, never another
// account's.
func TestAnAppConfirmationStampsOnlyTheCallersOwnSignIn(t *testing.T) {
	h := appHarness(t)
	ctx := t.Context()
	label := "Alice's phone"
	// Her phone signed in by password long ago, and a second sign-in beside it.
	hers, err := h.routes.Sessions.StartPhoneSession(ctx, testpostgres.Alice, &label, signin.ProvedByPassword)
	if err != nil {
		t.Fatal(err)
	}
	other, err := h.routes.Sessions.StartPhoneSession(ctx, testpostgres.Alice, &label, signin.ProvedByPassword)
	if err != nil {
		t.Fatal(err)
	}
	caller := principal.Principal{UserID: testpostgres.Alice, Method: principal.MethodSession, SessionID: *hers.SessionID}
	proved := func(family string) int {
		return h.count(`SELECT count(*) FROM refresh_tokens WHERE family_id = $1 AND sso_proved_at IS NOT NULL`, family)
	}

	mine, his := newPKCE(t), newPKCE(t)
	// Bob's identity in the sheet: a code for his account proves nothing
	// about her sign-in, and is spent.
	bobsCode := h.appSignIn(bob, his.challenge).Query().Get("code")
	refusedCode(t, "a code of another account", h.confirm(caller, bobsCode, his.verifier), http.StatusForbidden, codeAppOtherAccount)
	// Her code with the wrong verifier.
	herCode := h.appSignIn(alice, mine.challenge).Query().Get("code")
	refusedCode(t, "her code with his verifier", h.confirm(caller, herCode, his.verifier), http.StatusBadRequest, codeAppVerifier)
	refusedCode(t, "her code once spent", h.confirm(caller, herCode, mine.verifier), http.StatusBadRequest, codeAppExpired)
	// An API key has no sign-in to confirm.
	key := principal.Principal{UserID: testpostgres.Alice, Method: principal.MethodPersonalAccessToken}
	herCode = h.appSignIn(alice, mine.challenge).Query().Get("code")
	if got := h.confirm(key, herCode, mine.verifier); got.Code != http.StatusForbidden {
		t.Errorf("an API key confirmed: %d %s", got.Code, got.Body)
	}
	if proved(*hers.SessionID) != 0 || proved(*other.SessionID) != 0 {
		t.Fatal("a refused confirmation stamped a sign-in")
	}

	herCode = h.appSignIn(alice, mine.challenge).Query().Get("code")
	if got := h.confirm(caller, herCode, mine.verifier); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"confirmed":true`) {
		t.Fatalf("her confirmation answered %d %s", got.Code, got.Body)
	}
	if proved(*hers.SessionID) != 1 || proved(*other.SessionID) != 0 {
		t.Errorf("stamped: hers %d, her other sign-in %d", proved(*hers.SessionID), proved(*other.SessionID))
	}
	// The confirmation made no new sign-in.
	if n := h.count(`SELECT count(DISTINCT family_id) FROM refresh_tokens WHERE user_id = $1`, testpostgres.Alice); n != 2 {
		t.Errorf("she has %d sign-ins after confirming", n)
	}
}

// A sign-in through the provider is itself a proof: the app's exchange
// starts a phone sign-in stamped with the moment.
func TestAnAppSignInThroughTheProviderIsRecordedAsProved(t *testing.T) {
	h := appHarness(t)
	codes := newPKCE(t)
	code := h.appSignIn(alice, codes.challenge).Query().Get("code")
	got := h.exchange(code, codes.verifier, "")
	if got.Code != http.StatusOK {
		t.Fatalf("the exchange answered %d %s", got.Code, got.Body)
	}
	if n := h.count(`SELECT count(*) FROM refresh_tokens WHERE user_id = $1 AND sso_proved_at = $2`, testpostgres.Alice, h.clock); n != 1 {
		t.Errorf("%d of her sign-ins are recorded as proved by the provider now", n)
	}
}
