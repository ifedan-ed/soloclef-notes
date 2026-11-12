package agentsignin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// Accounts finds the account a verified identity acts as.
type Accounts interface {
	Account(ctx context.Context, identity Identity) (principal.AgentAccount, error)
}

// Principals turns an agent's bearer token into the account it acts as, or into
// a refusal, in the words app/agent_sign_in/principal.py used.
type Principals struct {
	Verifier *Verifier
	Accounts Accounts
	// BrandName is the product name two of the refusals name, mirroring
	// app.brand.BRAND_NAME.
	BrandName string
	Logger    *slog.Logger
}

// ErrSignInUnreachable is the answer when the provider's metadata or keys
// cannot be read, so the token could be checked neither way. The words are the
// ones agents have always been given for it.
var ErrSignInUnreachable = principal.Refusal{
	Status:  http.StatusServiceUnavailable,
	Message: "Single sign-on cannot be reached right now; try again shortly",
}

// OwnsIssuer is is_agent_token: whether this verifier is the one for a token
// claiming that issuer.
func (p Principals) OwnsIssuer(issuer string) bool {
	return p.Verifier != nil && p.Verifier.OwnsIssuer(issuer)
}

// Resolve is agent_user: the account the token acts as, a 401 for a token that
// is not a valid sign-in, a 403 when the provider does not let this person in or
// no account matches, and a 503 when the provider could not be read at all.
func (p Principals) Resolve(ctx context.Context, token string) (principal.AgentAccount, error) {
	identity, err := p.Verifier.Verify(ctx, token)
	switch {
	case errors.Is(err, ErrProviderUnavailable):
		// 503 and never 401. The token may be perfectly good: what failed is
		// reading the provider's metadata or keys. A 401 would make Claude and
		// ChatGPT drop the token and send the person back to sign in, for
		// every connected agent at once; a 503 says "try again shortly",
		// which is the truth.
		p.Logger.WarnContext(ctx, "an agent's sign-in could not be checked",
			slog.String("error", err.Error()))
		return principal.AgentAccount{}, ErrSignInUnreachable
	case errors.Is(err, ErrRefused):
		p.Logger.InfoContext(ctx, "an agent's sign-in token was refused",
			slog.String("error", err.Error()))
		return principal.AgentAccount{}, principal.Refusal{
			Status:  http.StatusUnauthorized,
			Message: "The agent's sign-in is not valid or has expired",
		}
	case err != nil:
		return principal.AgentAccount{}, err
	}

	if identity.NotesAccess == nil || !*identity.NotesAccess {
		// 403, not 401: signing in again as the same person cannot fix it, so
		// the client must not be sent back into a sign-in loop. A token without
		// the claim is refused too, failing closed: either the provider was set
		// up without the access mapping, or it failed.
		p.Logger.InfoContext(ctx, "an agent's sign-in was refused",
			slog.String("subject", identity.Subject),
			slog.Bool("claim_present", identity.NotesAccess != nil))
		return principal.AgentAccount{}, principal.Refusal{
			Status:  http.StatusForbidden,
			Message: "This sign-in may not use " + p.BrandName,
		}
	}

	account, err := p.Accounts.Account(ctx, identity)
	if errors.Is(err, ErrNoAccount) {
		p.Logger.InfoContext(ctx, "an agent's sign-in matches no account",
			slog.String("subject", identity.Subject),
			slog.String("error", err.Error()))
		return principal.AgentAccount{}, principal.Refusal{
			Status:  http.StatusForbidden,
			Message: "No " + p.BrandName + " account matches this sign-in",
		}
	}
	if err != nil {
		return principal.AgentAccount{}, err
	}
	return account, nil
}

var _ principal.Agents = Principals{}
