package signin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// presentedRefresh is the refresh token a request carries, and whether it
// came in the cookie. The cookie wins when both are sent: a browser that
// has one is a browser, whatever its script also posted.
func (r Routes) presentedRefresh(request *http.Request) (token string, fromCookie bool) {
	if cookie, err := request.Cookie(r.Cookies.RefreshName); err == nil && cookie.Value != "" {
		return cookie.Value, true
	}
	body, ok := readBody(request)
	if !ok || len(body) == 0 {
		return "", false
	}
	var asked struct {
		RefreshToken *string `json:"refresh_token"`
	}
	if json.Unmarshal(body, &asked) != nil || asked.RefreshToken == nil {
		return "", false
	}
	return strings.TrimSpace(*asked.RefreshToken), false
}

// refresh spends a refresh token for a new access token and, unless it is
// the grace case, the token's successor. The answer is cookies for a token
// that came in a cookie and a token pair for one that came in a body.
func (r Routes) refresh(response http.ResponseWriter, request *http.Request) {
	token, fromCookie := r.presentedRefresh(request)
	if token == "" {
		r.refuse(response, request, http.StatusUnauthorized, refreshRefused)
		return
	}
	now := r.now()
	granted, err := rotate(request.Context(), r.Pool, token, now)
	if errors.Is(err, errRefreshRefused) {
		r.refuse(response, request, http.StatusUnauthorized, refreshRefused)
		return
	}
	if err != nil {
		r.fail(response, request, "could not rotate the refresh token", err)
		return
	}
	// A deactivated (or deleted) account's sign-in does not outlive it.
	active, err := New(r.Pool).AccountIsActive(request.Context(), granted.userID)
	if err != nil && !isNoRows(err) {
		r.fail(response, request, "could not read the account", err)
		return
	}
	if !active {
		r.refuse(response, request, http.StatusUnauthorized, refreshRefused)
		return
	}
	// The same sign-in as the refresh token it spent, the grace case too:
	// the new access token ends when that sign-in does.
	access, expires, err := mintAccess(r.Secret, granted.userID, granted.familyID, now)
	if err != nil {
		r.fail(response, request, "could not sign the access token", err)
		return
	}
	if fromCookie {
		if granted.token == "" {
			r.Cookies.writeAccess(response, request, access, now)
		} else {
			r.Cookies.write(response, request, access, granted.token, now)
		}
		send(response, http.StatusOK, cookieSession{Authenticated: true, AccessExpiresAt: expires})
		return
	}
	pair := TokenPair{AccessToken: access, TokenType: "bearer", AccessExpiresAt: expires, SessionID: &granted.familyID}
	if granted.token != "" {
		pair.RefreshToken = &granted.token
	}
	send(response, http.StatusOK, pair)
}

// signOut ends the sign-in the refresh token belongs to and clears the
// cookies. It takes no access token -- signing out must work with an
// expired one -- and it answers the same whether or not the token named a
// sign-in, because a client signing out has nothing to do with the
// difference.
func (r Routes) signOut(response http.ResponseWriter, request *http.Request) {
	token, _ := r.presentedRefresh(request)
	now := r.now()
	if token != "" {
		if _, err := endByToken(request.Context(), r.Pool, token, now); err != nil {
			r.fail(response, request, "could not end the sign-in", err)
			return
		}
	}
	r.Cookies.clear(response, request, now)
	send(response, http.StatusOK, map[string]string{"detail": "Successfully logged out"})
}

// session is when the caller's access token expires, which the web app
// schedules its next refresh by. An API key or an agent has no session to
// expire and is answered null.
func (r Routes) session(response http.ResponseWriter, request *http.Request) {
	caller, ok := principal.FromContext(request.Context())
	if !ok {
		r.fail(response, request, "the route ran without an authenticated caller", nil)
		return
	}
	type answer struct {
		Authenticated   bool   `json:"authenticated"`
		AccessExpiresAt *int64 `json:"access_expires_at"`
	}
	if caller.IsGated() {
		send(response, http.StatusOK, answer{Authenticated: true})
		return
	}
	// The gate let the request in on one of these two; the cookie first,
	// as the gate reads them.
	token := ""
	if cookie, err := request.Cookie(r.Cookies.SessionName); err == nil {
		token = cookie.Value
	}
	if _, expires, found := r.sessionExpiry(token); found {
		send(response, http.StatusOK, answer{Authenticated: true, AccessExpiresAt: &expires})
		return
	}
	scheme, bearer, _ := strings.Cut(request.Header.Get("Authorization"), " ")
	if strings.EqualFold(scheme, "bearer") {
		if _, expires, found := r.sessionExpiry(bearer); found {
			send(response, http.StatusOK, answer{Authenticated: true, AccessExpiresAt: &expires})
			return
		}
	}
	r.refuse(response, request, http.StatusUnauthorized, "Unauthorized")
}

func (r Routes) sessionExpiry(token string) (string, int64, bool) {
	if token == "" {
		return "", 0, false
	}
	expires, ok := accessExpiry(r.Secret, token)
	return token, expires, ok
}
