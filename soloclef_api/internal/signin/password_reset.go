package signin

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

// A password reset by mail: `forgot-password` mails a link, and
// `reset-password` sets a new password for the token in it.
//
// # The token
//
// A short HS256 JWT over the session secret, under an audience of its own so
// it is never an access token and an access token is never one. It names the
// account and carries a fingerprint of the password hash that was in place
// when it was minted. Nothing is stored: a token works until its hour is up
// or the password changes, whichever is first, so it works once, and a
// second link asked for before the first is used does not cancel the first.
//
// # What it does better than the Python
//
//   - **A deployment that sends no mail says so.** The Python answered 202
//     and logged a warning, so the page told the person a mail was on its
//     way that never was. Here the refusal is the same for every address,
//     so it still says nothing about who has an account.
//   - **A reset verifies the address.** The token came by mail to it.
//   - **A reset ends every sign-in**, as the operator's set-password does: a
//     password is usually reset because somebody else may have had it.

// Mailer is what sends the reset mail: soloclef_mail's SMTP transport in
// production (cmd/api), a recorder in a test.
type Mailer interface {
	Send(ctx context.Context, message mailer.Message) error
}

const (
	// resetLifetime is how long a mailed link works.
	resetLifetime = time.Hour
	// resetAudience keeps a reset token and an access token from ever
	// being taken for each other.
	resetAudience = "soloclef:password-reset"
	// resetSendTimeout bounds the whole of the work done after the answer.
	resetSendTimeout = time.Minute

	resetUnavailable     = "PASSWORD_RESET_UNAVAILABLE"
	resetBadToken        = "RESET_PASSWORD_BAD_TOKEN"
	resetInvalidPassword = "RESET_PASSWORD_INVALID_PASSWORD"
)

// passwordFingerprint ties a reset token to the password it replaces. It is
// keyed, so the token says nothing about the stored hash to whoever reads
// the mail. An account with no password yet (one made by single sign-on)
// has the fingerprint of nothing, and gains a password by the same link.
func passwordFingerprint(secret string, hashed *string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("password-reset\x00"))
	if hashed != nil {
		mac.Write([]byte(*hashed))
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func mintResetToken(secret, userID string, hashed *string, now time.Time) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": userID,
		"aud": resetAudience,
		"pwd": passwordFingerprint(secret, hashed),
		"iat": now.Unix(),
		"exp": now.Add(resetLifetime).Unix(),
	}).SignedString([]byte(secret))
}

// readResetToken is the account a token names and the fingerprint it
// carries, when the token is this tier's and still inside its hour.
func readResetToken(secret, token string, now time.Time) (userID, fingerprint string, ok bool) {
	claims := jwt.MapClaims{}
	parsed, err := jwt.NewParser(
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithAudience(resetAudience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	).ParseWithClaims(token, claims, func(*jwt.Token) (any, error) { return []byte(secret), nil })
	if err != nil || !parsed.Valid {
		return "", "", false
	}
	userID, _ = claims["sub"].(string)
	fingerprint, _ = claims["pwd"].(string)
	return userID, fingerprint, userID != "" && fingerprint != ""
}

// forgotPassword answers 202 for every address and then, after the answer,
// mails the link if the address has an account that may sign in. The lookup
// is after the answer too, so how long the request takes says nothing about
// whether the address is known.
func (r Routes) forgotPassword(response http.ResponseWriter, request *http.Request) {
	if r.Mail == nil {
		r.refuse(response, request, http.StatusServiceUnavailable, resetUnavailable)
		return
	}
	body, ok := readBody(request)
	if !ok {
		r.refuse(response, request, http.StatusRequestEntityTooLarge, "The request is too large.")
		return
	}
	var asked struct {
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &asked); err != nil {
		r.refuse(response, request, http.StatusBadRequest, "The request is not JSON with an email.")
		return
	}
	if address, ok := usableAddress(asked.Email); ok {
		r.background(func() { r.mailResetLink(address) })
	}
	send(response, http.StatusAccepted, struct{}{})
}

func (r Routes) background(task func()) {
	if r.Background != nil {
		r.Background(task)
		return
	}
	go task()
}

// mailResetLink runs after the request it belongs to has been answered, so
// it has a context of its own. The token is never logged.
func (r Routes) mailResetLink(address string) {
	ctx, cancel := context.WithTimeout(context.Background(), resetSendTimeout)
	defer cancel()
	account, err := New(r.Pool).AccountForReset(ctx, address)
	if isNoRows(err) || (err == nil && !account.IsActive) {
		return
	}
	if err != nil {
		r.Logger.ErrorContext(ctx, "a reset mail was not sent: the account could not be read", "error", err.Error())
		return
	}
	token, err := mintResetToken(r.Secret, account.ID, account.HashedPassword, r.now())
	if err != nil {
		r.Logger.ErrorContext(ctx, "a reset mail was not sent: the token could not be signed", "error", err.Error())
		return
	}
	if err := r.Mail.Send(ctx, resetMessage(r.BrandName, r.FrontendURL, account.Email, token)); err != nil {
		r.Logger.ErrorContext(ctx, "a reset mail was not sent", "user_id", account.ID, "error", err.Error())
	}
}

// resetLink is the page that sets the new password. The token rides in the
// fragment, which a browser never sends to a server, so it stays out of
// access logs and Referer headers.
func resetLink(frontendURL, token string) string {
	return strings.TrimRight(frontendURL, "/") + "/reset-password#token=" + token
}

func resetMessage(brand, frontendURL, to, token string) mailer.Message {
	link := resetLink(frontendURL, token)
	minutes := int(resetLifetime / time.Minute)
	text := fmt.Sprintf("Someone asked to reset the password for your %s account (%s).\n\n"+
		"To choose a new password, open this link within %d minutes:\n\n%s\n\n"+
		"The link works once. If you did not ask for this, ignore this email: your password stays as it is.\n",
		brand, to, minutes, link)
	page := fmt.Sprintf(`<div style="font-family:system-ui,-apple-system,Segoe UI,sans-serif;font-size:15px;line-height:1.5;color:#1f2937;max-width:520px">`+
		`<p>Someone asked to reset the password for your %s account (%s).</p>`+
		`<p><a href="%s" style="display:inline-block;padding:10px 18px;background:#2f6fed;color:#ffffff;border-radius:6px;text-decoration:none">Choose a new password</a></p>`+
		`<p>The link works once and expires in %d minutes. If the button does not open, copy this address into your browser:<br>`+
		`<span style="word-break:break-all;color:#4b5563">%s</span></p>`+
		`<p>If you did not ask for this, ignore this email: your password stays as it is.</p></div>`,
		html.EscapeString(brand), html.EscapeString(to), html.EscapeString(link), minutes, html.EscapeString(link))
	return mailer.Message{
		To:       to,
		Subject:  "Reset your " + brand + " password",
		TextBody: text,
		HTMLBody: page,
	}
}

// errResetRefused is a token that no longer fits its account: the password
// changed since, or the account may not sign in.
var errResetRefused = errors.New("reset token refused")

// resetPassword sets the new password for a mailed token and ends every
// sign-in the account had.
func (r Routes) resetPassword(response http.ResponseWriter, request *http.Request) {
	body, ok := readBody(request)
	if !ok {
		r.refuse(response, request, http.StatusRequestEntityTooLarge, "The request is too large.")
		return
	}
	var asked struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(body, &asked); err != nil {
		r.refuse(response, request, http.StatusBadRequest, resetBadToken)
		return
	}
	now := r.now()
	// The signature first: it is cheap, and nothing below (a database
	// read, an Argon2id hash) is done for a token this tier did not mint.
	userID, fingerprint, ok := readResetToken(r.Secret, strings.TrimSpace(asked.Token), now)
	if !ok {
		r.refuse(response, request, http.StatusBadRequest, resetBadToken)
		return
	}
	ctx := request.Context()
	// Whether the token still fits the account: it may sign in, and its
	// password is the one the token was minted beside.
	fits := func(hashed *string, active bool) bool {
		return active && hmac.Equal([]byte(passwordFingerprint(r.Secret, hashed)), []byte(fingerprint))
	}
	account, err := New(r.Pool).AccountPasswordState(ctx, userID)
	if isNoRows(err) || (err == nil && !fits(account.HashedPassword, account.IsActive)) {
		r.refuse(response, request, http.StatusBadRequest, resetBadToken)
		return
	}
	if err != nil {
		r.fail(response, request, "could not read the account", err)
		return
	}
	if err := checkNewPassword(account.Email, asked.Password); err != nil {
		r.refuse(response, request, http.StatusBadRequest, resetInvalidPassword)
		return
	}
	// Hashed outside the transaction: it takes tens of milliseconds and
	// 64 MiB, and a row lock should not be held across it.
	hash, err := r.Hasher.Hash(ctx, asked.Password)
	if err != nil {
		r.fail(response, request, "could not hash the password", err)
		return
	}
	_, err = database.Write(ctx, r.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
		q := New(tx)
		// Checked again under the lock: of two requests spending one
		// token, the second finds the first one's password in place.
		locked, err := q.LockAccountPasswordState(ctx, userID)
		if isNoRows(err) || (err == nil && !fits(locked.HashedPassword, locked.IsActive)) {
			return struct{}{}, errResetRefused
		}
		if err != nil {
			return struct{}{}, err
		}
		if err := q.ResetAccountPassword(ctx, ResetAccountPasswordParams{HashedPassword: &hash, UserID: userID}); err != nil {
			return struct{}{}, err
		}
		if err := q.EndEverySignIn(ctx, EndEverySignInParams{EndedAt: now, UserID: userID}); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, q.ForgetEveryPushDevice(ctx, userID)
	})
	if errors.Is(err, errResetRefused) {
		r.refuse(response, request, http.StatusBadRequest, resetBadToken)
		return
	}
	if err != nil {
		r.fail(response, request, "could not set the new password", err)
		return
	}
	send(response, http.StatusOK, struct{}{})
}
