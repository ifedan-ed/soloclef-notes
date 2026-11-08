package principal

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
)

// ErrUnauthorized is the 401 the Python tier raises at the end of
// get_auth_context when nothing identified the caller.
var ErrUnauthorized = errors.New("unauthorized")

// ErrServedByPythonTier means the request carries a credential only the Python
// tier can check — today an identity provider's access token, which it verifies
// against the provider's keys (app/agent_sign_in). Refusing it here would log
// an agent out; accepting it unchecked would be a bypass. The request goes to
// the tier that can check it.
var ErrServedByPythonTier = errors.New("credential is verified by the Python tier")

// Directory is the account state a resolution needs. It is an interface so the
// resolution order can be tested without a database.
type Directory interface {
	// UserIsActive reports whether the account exists and is active. A missing
	// account and an inactive one are the same answer here, because
	// get_auth_context treats both as "no principal".
	UserIsActive(ctx context.Context, userID string) (bool, error)
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
	// a Refusal for a verdict it reached, and ErrServedByPythonTier for a token
	// it could not judge.
	Resolve(ctx context.Context, token string) (string, error)
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
				if owner.NeedsLastUsedTouch(now()) {
					r.Directory.TouchPersonalAccessToken(owner.TokenID)
				}
				return Principal{UserID: owner.UserID, Method: MethodPersonalAccessToken}, nil
			}
		}

		if isBearer {
			userID, err := r.SessionTokens.UserID(token)
			switch {
			case errors.Is(err, ErrForeignIssuer):
				// A token claiming an issuer is never one of this
				// application's own, so from here it is either an agent's
				// sign-in or a credential this tier cannot judge. Either way
				// it never falls through to the session check, exactly as
				// get_auth_context never does.
				return r.resolveAgent(ctx, token)
			case err == nil:
				active, err := r.Directory.UserIsActive(ctx, userID)
				if err != nil {
					return Principal{}, err
				}
				if active {
					return Principal{UserID: userID, Method: MethodSession}, nil
				}
			}
		}
	}

	// The session cookie only ever holds this application's own access token
	// (app.auth.session_cookies.write_session), so anything else in it is
	// simply not valid — there is no other tier to ask.
	if cookie, err := request.Cookie(r.SessionCookieName); err == nil && cookie.Value != "" {
		if userID, tokenErr := r.SessionTokens.UserID(cookie.Value); tokenErr == nil {
			active, err := r.Directory.UserIsActive(ctx, userID)
			if err != nil {
				return Principal{}, err
			}
			if active {
				return Principal{UserID: userID, Method: MethodSession}, nil
			}
		}
	}

	return Principal{}, ErrUnauthorized
}

// resolveAgent is get_auth_context's agent branch: a token the agent verifier
// owns becomes an agent principal or a refusal, and anything else goes to the
// tier that can check it.
//
// Reading the issuer here can only cost a hop, never a wrong verdict: a token
// whose issuer this tier fails to read is forwarded, and the Python tier reads
// it again with its own parser.
func (r Resolver) resolveAgent(ctx context.Context, token string) (Principal, error) {
	issuer, _ := unverifiedIssuer(token)
	if r.Agents == nil || !r.Agents.OwnsIssuer(issuer) {
		return Principal{}, ErrServedByPythonTier
	}
	userID, err := r.Agents.Resolve(ctx, token)
	if err != nil {
		return Principal{}, err
	}
	return Principal{UserID: userID, Method: MethodAgent}, nil
}
