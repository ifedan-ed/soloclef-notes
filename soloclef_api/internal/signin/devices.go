package signin

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// The signed-in devices: the list a person reads to see where their account
// is open, signing one of them out, and signing out everywhere.
//
//	GET    /auth/devices               every live sign-in of the account
//	DELETE /auth/devices/{session_id}  sign one device out
//	POST   /auth/jwt/logout-all        sign every device out, this one too
//
// A device is a sign-in: one refresh-token family (refresh_tokens.go). The
// phone app marks its own row by the session_id its sign-in answered.
//
// All three are a person's own decisions about their sessions, so they take
// an interactive session only, never an API key or an agent's token: a
// program acting for the account must not be able to list where the person
// is signed in, or sign them out of their phone.
const (
	DevicesPath   = "/auth/devices"
	DevicePath    = "/auth/devices/{session_id}"
	LogoutAllPath = "/auth/jwt/logout-all"
)

// sessionRequired is the refusal of a caller that is not a session.
const sessionRequired = "This needs a signed-in session, not an API key."

// sessionIDShape is a family id as this tier writes them (newFamilyID): a
// UUID in its canonical text. Anything else names no sign-in and is the
// 404, before Postgres is asked to cast it.
var sessionIDShape = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// deviceRead is one row of the devices list, as the app reads it.
type deviceRead struct {
	SessionID   string    `json:"session_id"`
	Kind        string    `json:"kind"`
	DeviceLabel *string   `json:"device_label"`
	SignedInAt  time.Time `json:"signed_in_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// sessionCaller is the caller of a devices route, or false with the refusal
// written.
func (r Routes) sessionCaller(response http.ResponseWriter, request *http.Request) (string, bool) {
	caller, ok := principal.FromContext(request.Context())
	if !ok {
		r.fail(response, request, "a devices route ran without an authenticated caller", nil)
		return "", false
	}
	if !caller.IsSession() {
		r.refuse(response, request, http.StatusForbidden, sessionRequired)
		return "", false
	}
	return caller.UserID, true
}

// devices lists the account's live sign-ins, newest use first.
func (r Routes) devices(response http.ResponseWriter, request *http.Request) {
	userID, ok := r.sessionCaller(response, request)
	if !ok {
		return
	}
	rows, err := New(r.Pool).SignedInDevices(request.Context(), SignedInDevicesParams{UserID: userID, Now: r.now()})
	if err != nil {
		r.fail(response, request, "could not list the signed-in devices", err)
		return
	}
	list := make([]deviceRead, 0, len(rows))
	for _, row := range rows {
		list = append(list, deviceRead{
			SessionID: row.FamilyID, Kind: row.Kind, DeviceLabel: row.DeviceLabel,
			SignedInAt: row.SignedInAt.UTC(), LastUsedAt: row.LastUsedAt.UTC(), ExpiresAt: row.ExpiresAt.UTC(),
		})
	}
	send(response, http.StatusOK, list)
}

// signOutDevice ends one of the account's sign-ins and forgets the phone
// that registered for pushes under it. A session id that is not one of the
// account's live sign-ins -- malformed, never made, already ended, or
// another account's -- is the same 404, so the route says nothing about
// anyone else's sessions.
func (r Routes) signOutDevice(response http.ResponseWriter, request *http.Request) {
	userID, ok := r.sessionCaller(response, request)
	if !ok {
		return
	}
	// The handler is given a rebuilt request with no path values (Mount),
	// so the id is the path's last segment, which the route's pattern has
	// already matched as exactly one.
	familyID := strings.ToLower(strings.TrimPrefix(request.URL.Path, DevicesPath+"/"))
	if !sessionIDShape.MatchString(familyID) {
		r.refuse(response, request, http.StatusNotFound, "Not found")
		return
	}
	now := r.now()
	ended, err := database.Write(request.Context(), r.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (bool, error) {
		q := New(tx)
		// EndFamily names the account beside the family, so a guessed id
		// of someone else's sign-in ends nothing.
		count, err := q.EndFamily(ctx, EndFamilyParams{EndedAt: now, FamilyID: familyID, UserID: userID})
		if err != nil || count == 0 {
			return false, err
		}
		return true, q.ForgetPushDevices(ctx, ForgetPushDevicesParams{UserID: userID, FamilyID: &familyID})
	})
	if err != nil {
		r.fail(response, request, "could not sign the device out", err)
		return
	}
	if !ended {
		r.refuse(response, request, http.StatusNotFound, "Not found")
		return
	}
	r.Logger.InfoContext(request.Context(), "a device was signed out", "user_id", userID, "session_id", familyID)
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusNoContent)
}

// logoutAll ends every sign-in the account has, on every device, this one
// included, and forgets every phone registered for its pushes. The caller's
// own cookies are cleared too, so a browser that asked is signed out at once
// rather than at its next refresh. The phone app sends its refresh token in
// the body as well; it is not needed (every family is ended) and is ignored.
func (r Routes) logoutAll(response http.ResponseWriter, request *http.Request) {
	userID, ok := r.sessionCaller(response, request)
	if !ok {
		return
	}
	now := r.now()
	_, err := database.Write(request.Context(), r.Pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (struct{}, error) {
		q := New(tx)
		if err := q.EndEverySignIn(ctx, EndEverySignInParams{EndedAt: now, UserID: userID}); err != nil {
			return struct{}{}, err
		}
		return struct{}{}, q.ForgetEveryPushDevice(ctx, userID)
	})
	if err != nil {
		r.fail(response, request, "could not sign every device out", err)
		return
	}
	r.Cookies.clear(response, request, now)
	r.Logger.InfoContext(request.Context(), "every device was signed out", "user_id", userID)
	send(response, http.StatusOK, map[string]string{"detail": "Successfully logged out from all devices"})
}
