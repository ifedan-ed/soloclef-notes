package principal

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// ErrUnauthorized is the 401 at the end of the resolution, when nothing
// identified the caller.
var ErrUnauthorized = errors.New("unauthorized")

// Directory is the account state a resolution needs. It is an interface so the
// resolution order can be tested without a database.
type Directory interface {
	// SessionIsLive reports whether an access token's sign-in may still act:
	// the account exists and is active, and the sign-in (sessionID, a
	// refresh-token family of that account) has not been ended -- signed
	// out, signed out from the devices list or everywhere, ended by a new
	// password or a replayed refresh token -- nor run past its own expiry.
	// Every "no" is the same answer: the token is not a principal.
	SessionIsLive(ctx context.Context, userID, sessionID string, now time.Time) (bool, error)
	// PersonalAccessTokenOwner looks a hashed API key up, as resolve_pat does.
	PersonalAccessTokenOwner(ctx context.Context, tokenHash string, now time.Time) (PersonalAccessTokenOwner, bool, error)
	// TouchPersonalAccessToken records that the key was used, after the
	// response; it must never fail a request.
	TouchPersonalAccessToken(tokenID int64)
}

// Resolver answers who a request is, in app.users.get_auth_context's order.
type Resolver struct {
	SessionTokens     SessionToken
	Directory         Directory
	SessionCookieName string
	Now               func() time.Time
}

// Resolve mirrors get_auth_context step for step, including its fall-throughs:
// an API key that does not resolve, or a session bearer token that does not
// verify, does not end the resolution — the session cookie is still tried.
func (r Resolver) Resolve(ctx context.Context, request *http.Request) (Principal, error) {
	now := time.Now
	if r.Now != nil {
		now = r.Now
	}

	if header := request.Header.Get("Authorization"); header != "" {
		scheme, credential, _ := strings.Cut(header, " ")
		isBearer := strings.EqualFold(scheme, "bearer") && credential != ""
		token := credential
		if !isBearer {
			token = strings.TrimSpace(header)
		}

		if isPersonalAccessToken(token) {
			owner, found, err := r.Directory.PersonalAccessTokenOwner(ctx, HashToken(token), now())
			if err != nil {
				return Principal{}, err
			}
			if found {
				// A live key of an account that has API access off is
				// refused outright. It does not fall through to the cookie:
				// the caller presented a key, and answering as the browser
				// session beside it would hide that the key does not work.
				// The use is not recorded either, since nothing was used.
				if !owner.APIAccessEnabled {
					return Principal{}, ErrAPIAccessOff
				}
				if owner.NeedsLastUsedTouch(now()) {
					r.Directory.TouchPersonalAccessToken(owner.TokenID)
				}
				return Principal{UserID: owner.UserID, Method: MethodPersonalAccessToken}, nil
			}
		}

		if isBearer {
			session, err := r.SessionTokens.Read(token)
			switch {
			case errors.Is(err, ErrForeignIssuer):
				// A token claiming an issuer is never one of this
				// application's own, and nothing else here accepts one:
				// an agent connects with an API key. It is a 401 and does
				// not fall through to the cookie, because a program that
				// sent a credential must be told that credential does not
				// work, not be answered quietly as the browser beside it.
				return Principal{}, ErrUnauthorized
			case err == nil:
				live, err := r.Directory.SessionIsLive(ctx, session.UserID, session.SessionID, now())
				if err != nil {
					return Principal{}, err
				}
				if live {
					return Principal{UserID: session.UserID, Method: MethodSession, SessionID: session.SessionID}, nil
				}
			}
		}
	}

	// The session cookie only ever holds this application's own access token,
	// so anything else in it is simply not valid.
	if cookie, err := request.Cookie(r.SessionCookieName); err == nil && cookie.Value != "" {
		if session, tokenErr := r.SessionTokens.Read(cookie.Value); tokenErr == nil {
			live, err := r.Directory.SessionIsLive(ctx, session.UserID, session.SessionID, now())
			if err != nil {
				return Principal{}, err
			}
			if live {
				return Principal{UserID: session.UserID, Method: MethodSession, SessionID: session.SessionID}, nil
			}
		}
	}

	return Principal{}, ErrUnauthorized
}
