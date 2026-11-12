package oidcsignin

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"
)

func codeOf(err error) string {
	var known *refusal
	if errors.As(err, &known) {
		return known.code
	}
	return ""
}

// The rule that says which account an identity signs in to, case by case.
func TestWhichAccountAnIdentitySignsInTo(t *testing.T) {
	verified := identity{subject: "s", email: "person@example.test", emailVerified: true}
	unverified := identity{subject: "s", email: "person@example.test"}
	nameless := identity{subject: "s", emailVerified: true}
	for _, test := range []struct {
		why     string
		who     identity
		found   candidates
		signup  bool
		how     match
		refusal string
	}{
		{"an identity seen before, whatever its address says now", unverified, candidates{linked: true}, false, matchLinked, ""},
		{"a first sign-in whose address a verified account has", verified, candidates{addressTaken: true, addressVerified: true}, false, matchByAddress, ""},
		{"a first sign-in whose address an unverified account has: it may be a stranger's", verified, candidates{addressTaken: true}, true, "", codeAccountNotVerified},
		{"a first sign-in the provider did not verify the address of", unverified, candidates{addressTaken: true, addressVerified: true}, true, "", codeEmailNotVerified},
		{"a first sign-in with no address at all", nameless, candidates{}, true, "", codeEmailNotVerified},
		{"a first sign-in with no account, where sign-up is off", verified, candidates{}, false, "", codeNoAccount},
		{"a first sign-in with no account, where sign-up is on", verified, candidates{}, true, matchNewAccount, ""},
	} {
		how, err := decide(test.who, test.found, test.signup)
		if how != test.how || codeOf(err) != test.refusal {
			t.Errorf("%s: got %q %v, want %q %q", test.why, how, err, test.how, test.refusal)
		}
	}
}

// A finished sign-in is delivered to a page of the web app and nowhere else.
func TestASignInLandsOnlyOnAPageOfTheWebApp(t *testing.T) {
	for requested, want := range map[string]string{
		"/notebooks/7":                  "/notebooks/7",
		"/notebooks/7?tab=chat&t=10:30": "/notebooks/7?tab=chat&t=10:30",
		"":                              defaultReturnPath,
		"notebooks":                     defaultReturnPath,
		"//evil.example/x":              defaultReturnPath,
		`/\evil.example`:                defaultReturnPath,
		"https://evil.example/":         defaultReturnPath,
		"/x:y":                          defaultReturnPath,
		"/a\\b":                         defaultReturnPath,
		"/a\r\nSet-Cookie: x=y":         defaultReturnPath,
		"/login?error=x":                defaultReturnPath,
		"/auth/oidc/authorize-redirect": defaultReturnPath,
		"/" + strings.Repeat("a", 2048): defaultReturnPath,
	} {
		if got := safeReturnPath(requested); got != want {
			t.Errorf("%q lands on %q, want %q", requested, got, want)
		}
	}
}

// The attempt comes back out of its cookie only for the browser that
// started it, with the state it was started with, inside its ten minutes.
func TestAnAttemptIsReopenedOnlyByItsOwnStateSecretAndTime(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	started, err := newAttempt("/notebooks/7")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := newAttempt("")
	if started.state == other.state || started.nonce == other.nonce || started.verifier == other.verifier {
		t.Fatal("two attempts share a secret")
	}
	// RFC 7636: a verifier of 43 to 128 unreserved characters, and the
	// challenge is 43.
	if n := len(started.verifier); n < 43 || n > 128 || len(started.challenge()) != 43 {
		t.Errorf("verifier of %d characters, challenge of %d", n, len(started.challenge()))
	}
	sealed, err := started.seal("the-secret", now)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := reopenAttempt(sealed, started.state, "the-secret", now.Add(9*time.Minute))
	if err != nil || reopened != started {
		t.Fatalf("reopened %+v, %v", reopened, err)
	}
	for why, try := range map[string]func() (attempt, error){
		"another attempt's state":   func() (attempt, error) { return reopenAttempt(sealed, other.state, "the-secret", now) },
		"no state":                  func() (attempt, error) { return reopenAttempt(sealed, "", "the-secret", now) },
		"no cookie":                 func() (attempt, error) { return reopenAttempt("", started.state, "the-secret", now) },
		"a cookie sealed elsewhere": func() (attempt, error) { return reopenAttempt(sealed, started.state, "another-secret", now) },
		"a cookie past its time": func() (attempt, error) {
			return reopenAttempt(sealed, started.state, "the-secret", now.Add(attemptLifetime+time.Second))
		},
	} {
		if _, err := try(); codeOf(err) != codeInvalidState {
			t.Errorf("%s: %v", why, err)
		}
	}
}

func TestTheAuthorizationRequestCarriesTheChallengeStateAndNonce(t *testing.T) {
	started, _ := newAttempt("")
	settings := Settings{ClientID: "notes client", RedirectURL: "https://notes.example/auth/oidc/callback"}
	for _, endpoint := range []string{"https://sso.example/authorize", "https://sso.example/authorize?tenant=a"} {
		address, err := url.Parse(authorizationURL(endpoint, settings, started))
		if err != nil {
			t.Fatal(err)
		}
		query := address.Query()
		for name, want := range map[string]string{
			"response_type": "code", "client_id": "notes client", "redirect_uri": settings.RedirectURL,
			"scope": "openid email profile", "state": started.state, "nonce": started.nonce,
			"code_challenge": started.challenge(), "code_challenge_method": "S256",
		} {
			if query.Get(name) != want {
				t.Errorf("%s: %s is %q, want %q", endpoint, name, query.Get(name), want)
			}
		}
		if strings.Contains(address.RawQuery, started.verifier) {
			t.Error("the verifier left with the browser")
		}
	}
}
