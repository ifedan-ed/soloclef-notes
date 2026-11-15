package signin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// A signed-in person sets a new password from where they are signed in:
//
//	GET  /auth/password         {"has_password": bool}: which form to show
//	POST /auth/change-password  {current_password, new_password}
//
// It exists because the owner's rule for the phone apps (2026-11-12) is that
// account security changes happen in the app, never by sending the person to
// the website: a phone that stays signed in for years is at least as secure
// as a browser, so the app is where a password is changed. The website's
// Settings, Security, calls it too.
//
// # Who may
//
// Only an interactive session, never an API key: a
// program acting for the account must not be able to take it over.
//
// An account with a password must give it (current_password). That is what
// stops whoever picks up an unlocked phone or a browser left open from
// locking the owner out.
//
// An account with no password -- in this tier exactly an account first made
// through single sign-on, which stores none (internal/oidcsignin's
// CreateAccount) -- has nothing to give. It may set one only from a sign-in
// that the identity provider proved in the last ten minutes
// (ssoProofWindow): a sign-in through the provider, or the phone app's
// confirmation of its existing sign-in through it (internal/oidcsignin,
// POST /api/v1/auth/oidc/app/confirm). The website confirms by signing the
// browser in through the provider again (StartWebSession ends the sign-in
// it replaces). That is as strong as the current password would be: the
// provider asked for its own credentials a moment ago.
// The confirmation exists so a phone signed in for years need not sign out
// (and lose its offline copies and push registration) to prove it.
//
// # What it does
//
// The new password is held to the sign-up rules (checkNewPassword). Every
// other sign-in of the account ends at once, with the pushes of every other
// phone, because a password is usually changed when somebody else may have
// had it; the sign-in that asked keeps working, so the person is not signed
// out of the device they just proved themselves on. The change and a refused
// current password are written to the account's activity log.
//
// cmd/api holds it to the sign-in routes' per-client limit
// (PasswordAttempts): it checks a password, so it must not be a way to guess
// one faster than the sign-in form.

// The two addresses, beside the other sign-in routes.
const (
	ChangePasswordPath = "/auth/change-password"
	// PasswordStatePath says whether the account has a password at all, so
	// the app asks for the current one only when there is one (an account
	// single sign-on made has none, and is asked to confirm instead).
	PasswordStatePath = "/auth/password"
)

// The refusals, in words the apps map to sentences.
const (
	// changeBadCurrent is a current password that is missing or wrong, or no
	// longer the account's (another change won the race).
	changeBadCurrent = "CHANGE_PASSWORD_BAD_CURRENT"
	// changeInvalidPassword is a new password the sign-up rules refuse.
	changeInvalidPassword = "CHANGE_PASSWORD_INVALID_PASSWORD"
	// changeConfirmSSO is an account with no password asked from a sign-in
	// the provider did not prove in the last ten minutes: the app confirms
	// through single sign-on and asks again.
	changeConfirmSSO = "CHANGE_PASSWORD_CONFIRM_SSO"
	// changeBadRequest is a body that is not the two passwords in JSON.
	changeBadRequest = "The request is not JSON with the passwords."
)

// activitySource is the activity log's "source" for what this package
// writes there: the account's own security changes.
const activitySource = "Account"

// errPasswordMoved is the account's password changing between the check of
// the current one and the write: of two changes racing, the second finds the
// first one's password and is refused, as a wrong current password is.
var errPasswordMoved = errors.New("the password changed while this change was checked")

// passwordChanged is the route's answer.
type passwordChanged struct {
	Detail string `json:"detail"`
	// SignedOutSessions is how many other sign-ins were ended, which the app
	// says ("Signed out of 2 other devices").
	SignedOutSessions int64 `json:"signed_out_sessions"`
}

// passwordState answers whether the caller's account has a password. Only
// a session may ask, as only a session may change it; the hash itself is
// never read out.
func (r Routes) passwordState(response http.ResponseWriter, request *http.Request) {
	userID, ok := r.sessionCaller(response, request)
	if !ok {
		return
	}
	account, err := New(r.Pool).AccountPasswordState(request.Context(), userID)
	if isNoRows(err) {
		r.refuse(response, request, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if err != nil {
		r.fail(response, request, "could not read the account", err)
		return
	}
	send(response, http.StatusOK, map[string]bool{"has_password": account.HashedPassword != nil})
}

func (r Routes) changePassword(response http.ResponseWriter, request *http.Request) {
	caller, ok := principal.FromContext(request.Context())
	if !ok {
		r.fail(response, request, "the password change ran without an authenticated caller", nil)
		return
	}
	// The sign-in to keep is the caller's own; a session's token always
	// names one (the gate refuses a token that names none).
	if !caller.IsSession() || caller.SessionID == "" {
		r.refuse(response, request, http.StatusForbidden, sessionRequired)
		return
	}
	body, ok := readBody(request)
	if !ok {
		r.refuse(response, request, http.StatusRequestEntityTooLarge, "The request is too large.")
		return
	}
	var asked struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := json.Unmarshal(body, &asked); err != nil {
		r.refuse(response, request, http.StatusBadRequest, changeBadRequest)
		return
	}
	ctx, userID, now := request.Context(), caller.UserID, r.now()

	account, err := New(r.Pool).AccountPasswordState(ctx, userID)
	if isNoRows(err) {
		// The gate found the account a moment ago; it was deleted since.
		r.refuse(response, request, http.StatusUnauthorized, "Unauthorized")
		return
	}
	if err != nil {
		r.fail(response, request, "could not read the account", err)
		return
	}
	// The rules first: they cost nothing, and a refused new password need not
	// spend an Argon2id verification on the current one.
	if err := checkNewPassword(account.Email, asked.NewPassword); err != nil {
		r.refuse(response, request, http.StatusBadRequest, changeInvalidPassword)
		return
	}

	if account.HashedPassword != nil {
		if !r.currentPasswordMatches(response, request, userID, asked.CurrentPassword, *account.HashedPassword, now) {
			return
		}
	} else if !r.provedBySSOJustNow(response, request, caller, now) {
		return
	}

	// Hashed outside the transaction: it takes tens of milliseconds and
	// 64 MiB, and a row lock should not be held across it.
	hash, err := r.Hasher.Hash(ctx, asked.NewPassword)
	if err != nil {
		r.fail(response, request, "could not hash the password", err)
		return
	}
	ended, err := database.Write(ctx, r.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (int64, error) {
		q := New(tx)
		// Checked again under the lock: the password checked above must
		// still be the account's, or another change got there first.
		locked, err := q.LockAccountPasswordState(ctx, userID)
		if err != nil {
			return 0, err
		}
		if !samePassword(locked.HashedPassword, account.HashedPassword) {
			return 0, errPasswordMoved
		}
		if err := q.ChangeAccountPassword(ctx, ChangeAccountPasswordParams{HashedPassword: &hash, ChangedAt: now, UserID: userID}); err != nil {
			return 0, err
		}
		ended, err := q.EndOtherSignIns(ctx, EndOtherSignInsParams{EndedAt: now, UserID: userID, KeptFamilyID: caller.SessionID})
		if err != nil {
			return 0, err
		}
		if err := q.ForgetOtherPushDevices(ctx, ForgetOtherPushDevicesParams{UserID: userID, KeptFamilyID: caller.SessionID}); err != nil {
			return 0, err
		}
		// In the same transaction, so the log says it exactly when it
		// happened.
		return ended, writeAccountLog(ctx, q, userID, LoglevelINFO, LogstatusSUCCESS,
			changedMessage(account.HashedPassword == nil, ended),
			map[string]any{"event": "password_changed", "first_password": account.HashedPassword == nil,
				"signed_out_sessions": ended, "kept_session_id": caller.SessionID}, now)
	})
	if errors.Is(err, errPasswordMoved) || isNoRows(err) {
		r.refuse(response, request, http.StatusBadRequest, changeBadCurrent)
		return
	}
	if err != nil {
		r.fail(response, request, "could not change the password", err)
		return
	}
	r.Logger.InfoContext(ctx, "a password was changed", "user_id", userID, "session_id", caller.SessionID, "signed_out_sessions", ended)
	send(response, http.StatusOK, passwordChanged{Detail: "Password changed", SignedOutSessions: ended})
}

// currentPasswordMatches checks the current password the person typed, or
// writes the refusal (and a line in their activity log) and answers false.
func (r Routes) currentPasswordMatches(response http.ResponseWriter, request *http.Request, userID, current, stored string, now time.Time) bool {
	ctx := request.Context()
	// A password longer than any may be cannot match, and is not hashed:
	// that would be work done for whoever sent it.
	if current == "" || utf8.RuneCountInString(current) > maxPasswordLength {
		r.refuse(response, request, http.StatusBadRequest, changeBadCurrent)
		return false
	}
	matches, _, err := r.Hasher.Verify(ctx, current, stored)
	if err != nil {
		r.fail(response, request, "could not verify the current password", err)
		return false
	}
	if !matches {
		// Written to the person's own log: a wrong current password from one
		// of their signed-in devices is something they should be able to see.
		// The log is a courtesy and never changes the answer.
		if err := writeAccountLog(ctx, New(r.Pool), userID, LoglevelWARNING, LogstatusFAILED,
			"A password change was refused: the current password was wrong.",
			map[string]any{"event": "password_change_refused"}, now); err != nil {
			r.Logger.WarnContext(ctx, "could not log a refused password change", "user_id", userID, "error", err.Error())
		}
		r.refuse(response, request, http.StatusBadRequest, changeBadCurrent)
		return false
	}
	return true
}

// provedBySSOJustNow reports whether the caller's own sign-in was proved by
// the identity provider within ssoProofWindow, or writes the refusal and
// answers false. It is asked only for an account with no password.
func (r Routes) provedBySSOJustNow(response http.ResponseWriter, request *http.Request, caller principal.Principal, now time.Time) bool {
	proved, err := New(r.Pool).SessionSSOProof(request.Context(), SessionSSOProofParams{
		UserID: caller.UserID, FamilyID: caller.SessionID, Now: now})
	if isNoRows(err) {
		// The sign-in ended between the gate and here.
		r.refuse(response, request, http.StatusUnauthorized, "Unauthorized")
		return false
	}
	if err != nil {
		r.fail(response, request, "could not read how the sign-in was proved", err)
		return false
	}
	// A proof from the future is a clock that moved back, and is no proof.
	if proved == nil || proved.After(now) || now.Sub(*proved) > ssoProofWindow {
		r.refuse(response, request, http.StatusForbidden, changeConfirmSSO)
		return false
	}
	return true
}

// ConfirmSessionBySSO records that the identity provider has just proved who
// holds this sign-in of the account, without starting a new one: the phone
// app's confirmation (internal/oidcsignin). It answers false when the
// sign-in is over, so nothing was stamped.
func (r Routes) ConfirmSessionBySSO(ctx context.Context, userID, sessionID string) (bool, error) {
	now := r.now()
	stamped, err := New(r.Pool).ProveSessionBySSO(ctx, ProveSessionBySSOParams{ProvedAt: &now, UserID: userID, FamilyID: sessionID})
	return stamped > 0, err
}

// samePassword reports whether two reads of the stored hash are the same
// password: both none, or the same hash.
func samePassword(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// changedMessage is the activity log's sentence for a change.
func changedMessage(first bool, ended int64) string {
	what := "Password changed."
	if first {
		what = "Password set."
	}
	switch ended {
	case 0:
		return what + " No other device was signed in."
	case 1:
		return what + " 1 other sign-in was signed out."
	default:
		return fmt.Sprintf("%s %d other sign-ins were signed out.", what, ended)
	}
}

// writeAccountLog writes one entry in the account's activity log.
func writeAccountLog(ctx context.Context, q *Queries, userID string, level Loglevel, status Logstatus, message string, metadata map[string]any, now time.Time) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	source := activitySource
	return q.LogAccountEvent(ctx, LogAccountEventParams{
		UserID: userID, Level: level, Status: status, Message: message,
		Source: &source, LogMetadata: encoded, CreatedAt: now,
	})
}
