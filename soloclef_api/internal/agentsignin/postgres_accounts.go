package agentsignin

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// PostgresAccounts finds the account a verified identity acts as, read-only, as
// account.account_for_agent does.
type PostgresAccounts struct {
	Pool *pgxpool.Pool
	// ProviderName is the oauth_account.oauth_name the web sign-in links under.
	ProviderName string
}

// Account returns the account and its API-access switch, ErrNoAccount when the
// rule names none and when the account it names is gone or deactivated, or a
// database error.
func (a PostgresAccounts) Account(ctx context.Context, identity Identity) (principal.AgentAccount, error) {
	queries := New(a.Pool)

	linked, err := queries.LinkedAccount(ctx, LinkedAccountParams{
		OauthName: a.ProviderName,
		AccountID: identity.Subject,
	})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return principal.AgentAccount{}, fmt.Errorf("reading the identity's link: %w", err)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		linked = ""
	}

	candidates := Candidates{LinkedUserID: linked, EmailVerified: identity.EmailVerified}
	var sameEmail AccountWithEmailRow
	if linked == "" && identity.Email != "" {
		sameEmail, err = queries.AccountWithEmail(ctx, identity.Email)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return principal.AgentAccount{}, fmt.Errorf("reading the account with that address: %w", err)
		}
		if err == nil {
			candidates.EmailUserID = sameEmail.ID
			candidates.EmailUserVerified = sameEmail.IsVerified
		}
	}

	chosen, err := ChooseAccount(candidates)
	if err != nil {
		return principal.AgentAccount{}, err
	}

	// The row the account was chosen from is the row its state is read from,
	// as there: only a linked account needs a second read.
	active, apiAccess := sameEmail.IsActive, sameEmail.ApiAccessEnabled
	if chosen != sameEmail.ID {
		standing, err := queries.AccountStanding(ctx, chosen)
		if errors.Is(err, pgx.ErrNoRows) {
			return principal.AgentAccount{}, fmt.Errorf("%w: the account is gone", ErrNoAccount)
		}
		if err != nil {
			return principal.AgentAccount{}, fmt.Errorf("reading whether the account is active: %w", err)
		}
		active, apiAccess = standing.IsActive, standing.ApiAccessEnabled
	}
	if !active {
		return principal.AgentAccount{}, fmt.Errorf("%w: the account is deactivated", ErrNoAccount)
	}
	return principal.AgentAccount{UserID: chosen, APIAccessEnabled: apiAccess}, nil
}
