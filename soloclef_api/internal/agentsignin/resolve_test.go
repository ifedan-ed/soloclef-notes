package agentsignin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

type fakeAccounts struct {
	account string
	err     error
	asked   []Identity
}

func (a *fakeAccounts) Account(_ context.Context, identity Identity) (string, error) {
	a.asked = append(a.asked, identity)
	return a.account, a.err
}

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The words and the statuses are the Python tier's, because a client reads them:
// a 401 sends an agent back through a sign-in, and a 403 must not.
func TestTheRefusalsAreThePythonTiersOwn(t *testing.T) {
	signing := newRSAKey(t)
	now := time.Now()

	cases := map[string]struct {
		claims   func(tokenClaims)
		accounts *fakeAccounts
		status   int
		message  string
	}{
		"a sign-in the provider no longer allows": {
			func(claims tokenClaims) { claims[NotesAccessClaim] = false },
			&fakeAccounts{account: "11111111-1111-1111-1111-111111111111"},
			http.StatusForbidden, "This sign-in may not use Soloclef Notes",
		},
		"a token with no access claim at all": {
			func(claims tokenClaims) { delete(claims, NotesAccessClaim) },
			&fakeAccounts{account: "11111111-1111-1111-1111-111111111111"},
			http.StatusForbidden, "This sign-in may not use Soloclef Notes",
		},
		"a sign-in with no account": {
			func(tokenClaims) {},
			&fakeAccounts{err: ErrNoAccount},
			http.StatusForbidden, "No Soloclef Notes account matches this sign-in",
		},
		"an expired token": {
			func(claims tokenClaims) { claims["exp"] = time.Now().Add(-time.Hour).Unix() },
			&fakeAccounts{account: "11111111-1111-1111-1111-111111111111"},
			http.StatusUnauthorized, "The agent's sign-in is not valid or has expired",
		},
	}

	for name, test := range cases {
		provider := newFakeProvider(t, keySet(t, publicJWK(t, "signing", &signing.PublicKey)))
		principals := Principals{
			Verifier:  provider.verifier(nil),
			Accounts:  test.accounts,
			BrandName: "Soloclef Notes",
			Logger:    quietLogger(),
		}
		claims := validClaims(provider.issuer, now)
		test.claims(claims)

		_, err := principals.Resolve(context.Background(),
			mint(t, signing, "signing", jwt.SigningMethodRS256, claims))
		var refusal principal.Refusal
		if !errors.As(err, &refusal) {
			t.Errorf("%s: got %v, want a refusal", name, err)
			continue
		}
		if refusal.Status != test.status || refusal.Message != test.message {
			t.Errorf("%s: got %d %q, want %d %q",
				name, refusal.Status, refusal.Message, test.status, test.message)
		}
	}
}

func TestAVerifiedSignInActsAsItsAccount(t *testing.T) {
	signing := newRSAKey(t)
	provider := newFakeProvider(t, keySet(t, publicJWK(t, "signing", &signing.PublicKey)))
	accounts := &fakeAccounts{account: "11111111-1111-1111-1111-111111111111"}
	principals := Principals{
		Verifier:  provider.verifier(nil),
		Accounts:  accounts,
		BrandName: "Soloclef Notes",
		Logger:    quietLogger(),
	}

	account, err := principals.Resolve(context.Background(),
		mint(t, signing, "signing", jwt.SigningMethodRS256, validClaims(provider.issuer, time.Now())))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if account != accounts.account {
		t.Errorf("got %q, want %q", account, accounts.account)
	}
	if len(accounts.asked) != 1 || accounts.asked[0].Subject != "provider-subject-1" {
		t.Errorf("the account was looked up as %v", accounts.asked)
	}
}

// A provider this tier cannot read is not a verdict. The Python tier keeps its
// own copy of the provider's keys, so the request goes there rather than being
// answered with a guess.
func TestAProviderThatCannotBeReadIsServedByThePythonTier(t *testing.T) {
	signing := newRSAKey(t)
	provider := newFakeProvider(t, keySet(t, publicJWK(t, "signing", &signing.PublicKey)))
	provider.fail.Store(true)
	principals := Principals{
		Verifier:  provider.verifier(nil),
		Accounts:  &fakeAccounts{},
		BrandName: "Soloclef Notes",
		Logger:    quietLogger(),
	}

	_, err := principals.Resolve(context.Background(),
		mint(t, signing, "signing", jwt.SigningMethodRS256, validClaims(provider.issuer, time.Now())))
	if !errors.Is(err, principal.ErrServedByPythonTier) {
		t.Fatalf("got %v, want the request handed to the Python tier", err)
	}
}
