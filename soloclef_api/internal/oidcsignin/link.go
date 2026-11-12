package oidcsignin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
)

// Which account a verified identity signs in to.
//
//  1. An identity seen before signs in to the account it was linked to,
//     found by the provider's subject id, whatever its address says today.
//  2. A first-time identity is linked to the account with the same address,
//     but only when the provider says it verified that address and the
//     account's own address was verified too (made by the operator, by an
//     earlier single sign-on, or by a reset mail). Otherwise anyone who
//     signed up with another person's address would have that person's
//     first single sign-on land in an account they hold the password of;
//     that person is refused instead and the operator settles it.
//  3. Otherwise a new account is made, if the deployment lets people make
//     their own (SIGNUP_ALLOWED), and refused if not: the operator makes
//     the account and the next sign-in links to it by rule 2.

// identity is what a verified ID token says about the person.
type identity struct {
	subject string
	// email is lowercased and trimmed; empty when the token carries none.
	email string
	// emailVerified is true only for the JSON true.
	emailVerified bool
	name          string
	nonce         string
}

func identityFrom(claims map[string]json.RawMessage) identity {
	text := func(name string) string {
		var value string
		_ = json.Unmarshal(claims[name], &value)
		return strings.TrimSpace(value)
	}
	who := identity{
		subject: text("sub"),
		email:   strings.ToLower(text("email")),
		name:    text("name"),
		nonce:   text("nonce"),
	}
	_ = json.Unmarshal(claims["email_verified"], &who.emailVerified)
	// A name is shown beside the person's notes; it is not a place to keep
	// whatever length a provider sends.
	if runes := []rune(who.name); len(runes) > 255 {
		who.name = string(runes[:255])
	}
	return who
}

// match is how an identity found its account.
type match string

const (
	matchLinked     match = "linked"      // this identity signed in before
	matchByAddress  match = "by_address"  // first time; a verified account has its verified address
	matchNewAccount match = "new_account" // first time; no account had the address
)

// candidates is what the database knows about an identity, for decide.
type candidates struct {
	// linked says the identity has a link already.
	linked bool
	// addressTaken says an account has the identity's address, and
	// addressVerified that the account's own address was verified.
	addressTaken    bool
	addressVerified bool
}

// decide is the rule at the top of this file, with no database in the way.
func decide(who identity, found candidates, signupAllowed bool) (match, error) {
	if found.linked {
		return matchLinked, nil
	}
	if who.email == "" || !who.emailVerified {
		return "", refused(codeEmailNotVerified, "the provider has not verified an address for this identity")
	}
	if found.addressTaken {
		if !found.addressVerified {
			return "", refused(codeAccountNotVerified, "the account with this address never verified it")
		}
		return matchByAddress, nil
	}
	if !signupAllowed {
		return "", refused(codeNoAccount, "no account has this address and sign-up is off")
	}
	return matchNewAccount, nil
}

// accountFor settles the account in one transaction: the lookup, a new
// account if one is due, the link and the sign-in stamp commit together or
// not at all.
func accountFor(ctx context.Context, pool *pgxpool.Pool, settings Settings, who identity, now time.Time) (string, match, error) {
	if who.subject == "" {
		return "", "", refused(codeInvalidIDToken, "the ID token names no subject")
	}
	type outcome struct {
		userID string
		how    match
	}
	settled, err := database.Write(ctx, pool, func(ctx context.Context, tx pgx.Tx, _ *database.Effects) (outcome, error) {
		q := New(tx)
		userID, active, how, err := lookUp(ctx, q, settings, who)
		if err != nil {
			return outcome{}, err
		}
		if how == matchNewAccount {
			var name *string
			if who.name != "" {
				name = &who.name
			}
			userID, err = q.CreateAccount(ctx, CreateAccountParams{Email: who.email, DisplayName: name})
			if errors.Is(err, pgx.ErrNoRows) {
				// Another first sign-in with this address (a second tab, a
				// double click) or a sign-up made the account a moment ago.
				// It is judged as any account found by its address is: one
				// the sign-up form made is unverified and is not linked.
				userID, active, how, err = lookUp(ctx, q, settings, who)
				if err == nil && how == matchNewAccount {
					err = errors.New("the address was taken and then was not")
				}
			} else {
				active = true
			}
			if err != nil {
				return outcome{}, err
			}
		}
		if !active {
			return outcome{}, refused(codeInactive, "the account is deactivated")
		}
		if how == matchLinked {
			if who.email != "" {
				err = q.RefreshLinkAddress(ctx, RefreshLinkAddressParams{
					Email: who.email, Provider: settings.ProviderName, Subject: who.subject, UserID: userID})
			}
		} else {
			err = q.LinkIdentity(ctx, LinkIdentityParams{
				UserID: userID, Provider: settings.ProviderName, Subject: who.subject, Email: who.email})
		}
		if err != nil {
			return outcome{}, err
		}
		return outcome{userID: userID, how: how},
			q.RecordSignIn(ctx, RecordSignInParams{SignedInAt: &now, Name: who.name, UserID: userID})
	})
	return settled.userID, settled.how, err
}

// lookUp reads what the database knows about the identity and applies the
// rule. For matchNewAccount there is no account yet and userID is empty.
func lookUp(ctx context.Context, q *Queries, settings Settings, who identity) (userID string, active bool, how match, err error) {
	linked, err := q.LinkedAccount(ctx, LinkedAccountParams{Provider: settings.ProviderName, Subject: who.subject})
	switch {
	case err == nil:
		return linked.ID, linked.IsActive, matchLinked, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return "", false, "", err
	}
	var found candidates
	var sameAddress AccountWithAddressRow
	if who.email != "" {
		sameAddress, err = q.AccountWithAddress(ctx, who.email)
		switch {
		case err == nil:
			found.addressTaken, found.addressVerified = true, sameAddress.IsVerified
		case !errors.Is(err, pgx.ErrNoRows):
			return "", false, "", err
		}
	}
	how, err = decide(who, found, settings.SignupAllowed)
	if err != nil {
		return "", false, "", err
	}
	return sameAddress.ID, sameAddress.IsActive, how, nil
}
