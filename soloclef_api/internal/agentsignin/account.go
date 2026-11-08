package agentsignin

import "fmt"

// Candidates is what the two lookups found, so the rule that picks between them
// can be read and tested without a database — as account.choose_account can.
type Candidates struct {
	// LinkedUserID is the account this identity's subject was linked to by an
	// earlier web sign-in, empty when it was never linked.
	LinkedUserID string
	// EmailUserID is the account holding the same address, empty when there is
	// none.
	EmailUserID string
	// EmailVerified is whether the provider says it verified the address.
	EmailVerified bool
	// EmailUserVerified is whether that account's own address is verified.
	EmailUserVerified bool
}

// ChooseAccount is account.choose_account:
//
//  1. An identity the web sign-in linked before acts as that account, found by
//     the provider's subject id, whatever its email says today.
//  2. Otherwise the account with the same address, but only when the provider
//     verified that address and that account's own address is verified too — so
//     an account someone signed up with another person's address acts for
//     nobody.
//  3. Otherwise nobody. An agent sign-in never creates an account and never
//     records a link.
func ChooseAccount(candidates Candidates) (string, error) {
	if candidates.LinkedUserID != "" {
		return candidates.LinkedUserID, nil
	}
	if candidates.EmailUserID != "" && candidates.EmailVerified && candidates.EmailUserVerified {
		return candidates.EmailUserID, nil
	}
	return "", fmt.Errorf("%w: no account matches this sign-in", ErrNoAccount)
}
