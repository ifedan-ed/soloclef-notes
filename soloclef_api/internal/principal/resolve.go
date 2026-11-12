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

// Agents verifies an AI agent's access token from the identity provider. It is
// an interface so that the resolution order does not depend on the provider
// being reachable, and so a deployment with no agent provider configured simply
// has none (internal/agentsignin).
type Agents interface {
	// OwnsIssuer reports whether a token claiming this issuer is one this
	// verifier is for, as is_agent_token does before anything is checked.
	OwnsIssuer(issuer string) bool
	// Resolve verifies the token and returns the account it acts as. It returns
	// a Refusal for every verdict, a provider that cannot be read included
	// (a 503: the token may be perfectly good, so it is not a 401).
	Resolve(ctx context.Context, token string) (AgentAccount, error)
}

// AgentAccount is the account an agent's token acts as, with the account's
// API-access switch as it was on the row the account was found by.
type AgentAccount struct {
	UserID           string
	APIAccessEnabled bool
}

// Resolver answers who a request is, in app.users.get_auth_context's order.
type Resolver struct {
	SessionTokens     SessionToken
	Directory         Directory
	Agents            Agents
	SessionCookieName string
	Now               func() time.Time
}

// Resolve mirrors get_auth_context step for step, including its fall-throughs:
// an API key that does not resolve, or a bearer token that does not verify,
// does not end the resolution — the session cookie is still tried.
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
				// application's own, so from here it is an agent's sign-in
				// or nothing. Either way it never falls through to the
				// session check: a cookie sent beside a bad agent token must
				// not quietly answer as the browser's account.
				return r.resolveAgent(ctx, token)
			case err == nil:
				live, err := r.Directory.SessionIsLive(ctx, session.UserID, session.SessionID, now())
				if err != nil {
					return Principal{}, err
				}
				if live {
					return Principal{UserID: session.UserID, Method: MethodSession}, nil
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
				return Principal{UserID: session.UserID, Method: MethodSession}, nil
			}
		}
	}

	return Principal{}, ErrUnauthorized
}

// resolveAgent is the agent branch: a token the agent verifier owns becomes an
// agent principal or a refusal, and any other token that names an issuer is a
// 401. Nothing else in this process can verify such a token, so there is no one
// to hand it to; answering 401 tells the client, in the standard way, that the
// credential is not accepted here.
//
// Reading the issuer unverified is safe for the same reason: it only chooses
// which check runs, and a token whose issuer cannot be read is refused.
func (r Resolver) resolveAgent(ctx context.Context, token string) (Principal, error) {
	issuer, _ := unverifiedIssuer(token)
	if r.Agents == nil || !r.Agents.OwnsIssuer(issuer) {
		return Principal{}, ErrUnauthorized
	}
	account, err := r.Agents.Resolve(ctx, token)
	if err != nil {
		return Principal{}, err
	}
	// After every other verdict, so a token that is invalid or matches no
	// account is told that, and only a token that would otherwise have been
	// let in learns the switch is off.
	if !account.APIAccessEnabled {
		return Principal{}, ErrAPIAccessOff
	}
	return Principal{UserID: account.UserID, Method: MethodAgent}, nil
}
