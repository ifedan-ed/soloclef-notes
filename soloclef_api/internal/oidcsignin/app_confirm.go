package oidcsignin

import (
	"encoding/json"
	"io"
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// The phone app's confirmation through single sign-on.
//
//	POST /api/v1/auth/oidc/app/confirm {code, verifier}   (signed in)
//	  -> 200 {"confirmed": true}
//
// It is the app's sign-in (app_sign_in.go) up to its last step: the same
// start, the same browser sheet, the same callback and one-time code bound to
// the app's PKCE challenge. Where the exchange would spend the code on a new
// sign-in, this spends it on the sign-in the app already has, recording that
// the provider proved the person just now (internal/signin,
// ConfirmSessionBySSO).
//
// It exists for one thing: an account first made through single sign-on has
// no password, so it may set one only from a sign-in the provider proved in
// the last ten minutes (internal/signin, password_change.go). A phone stays
// signed in for years, and signing out to sign in again would cost it its
// offline copies and its push registration; confirming costs it nothing.
//
// The code must stand for the caller's own account. A code another account
// signed in for proves nothing about this sign-in and is refused, and spent:
// a code is spent once whoever presents it, as at the exchange.

// AppConfirmPath is the confirmation's address, beside the app's exchange.
const AppConfirmPath = "/api/v1/auth/oidc/app/confirm"

// codeAppOtherAccount is a code the provider issued for another account than
// the one signed in: the person chose another identity in the sheet.
const codeAppOtherAccount = "APP_CONFIRM_OTHER_ACCOUNT"

func (r Routes) appConfirm(response http.ResponseWriter, request *http.Request) {
	if r.off(response, request) {
		return
	}
	requestID := requestcontext.IDFrom(request)
	refuse := func(status int, code string) {
		response.Header().Set("Cache-Control", "no-store")
		// The app reads the code from the message, as at the exchange.
		r.Errors.WriteCoded(response, status, code, code, requestID)
	}
	caller, ok := principal.FromContext(request.Context())
	if !ok || !caller.IsSession() || caller.SessionID == "" {
		// Only the person's own sign-in can be confirmed: an API key has
		// none.
		refuse(http.StatusForbidden, "SESSION_REQUIRED")
		return
	}
	var asked struct {
		Code     string `json:"code"`
		Verifier string `json:"verifier"`
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, exchangeBodyMax+1))
	if err != nil || len(body) > exchangeBodyMax || json.Unmarshal(body, &asked) != nil ||
		asked.Code == "" || len(asked.Code) > 256 {
		refuse(http.StatusBadRequest, codeAppExpired)
		return
	}
	grant, found, err := r.Codes.Redeem(request.Context(), asked.Code)
	if err != nil {
		r.Logger.ErrorContext(request.Context(), "the app's one-time code could not be read", "request_id", requestID, "error", err.Error())
		refuse(http.StatusServiceUnavailable, codeUnavailable)
		return
	}
	if !found {
		refuse(http.StatusBadRequest, codeAppExpired)
		return
	}
	if !verifierMatches(asked.Verifier, grant.Challenge) {
		r.Logger.WarnContext(request.Context(), "an app confirmation with a verifier that is not the challenge's", "request_id", requestID)
		refuse(http.StatusBadRequest, codeAppVerifier)
		return
	}
	if grant.UserID != caller.UserID {
		r.Logger.WarnContext(request.Context(), "an app confirmation through another account's sign-in",
			"request_id", requestID, "user_id", caller.UserID)
		refuse(http.StatusForbidden, codeAppOtherAccount)
		return
	}
	stamped, err := r.Sessions.ConfirmSessionBySSO(request.Context(), caller.UserID, caller.SessionID)
	if err != nil {
		r.Logger.ErrorContext(request.Context(), "an app confirmation could not be recorded", "request_id", requestID, "error", err.Error())
		refuse(http.StatusInternalServerError, codeServerError)
		return
	}
	if !stamped {
		// The sign-in ended between the gate and here.
		refuse(http.StatusUnauthorized, "SESSION_ENDED")
		return
	}
	r.Logger.InfoContext(request.Context(), "app single sign-on confirmed a sign-in", "user_id", caller.UserID, "session_id", caller.SessionID)
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte(`{"confirmed":true}` + "\n"))
}
