package accesstokens

import (
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// A key is the gate's prefix and 256 random bits, URL-safe; what is stored
// of it is its hash and its first sixteen characters, and no two are alike.
func TestAMintedKeyIsStoredOnlyAsItsHashAndPrefix(t *testing.T) {
	seen := map[string]bool{}
	for range 64 {
		minted, err := MintKey()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(minted.Key, principal.TokenPrefix) || len(minted.Key) != len(principal.TokenPrefix)+43 {
			t.Fatalf("key %q is not the prefix and 43 URL-safe characters", minted.Key)
		}
		if strings.ContainsAny(minted.Key, "+/=") {
			t.Fatalf("key %q is not URL-safe", minted.Key)
		}
		if minted.Hash != principal.HashToken(minted.Key) || len(minted.Hash) != 64 {
			t.Fatalf("hash %q is not the gate's hash of the key", minted.Hash)
		}
		if minted.Prefix != minted.Key[:16] {
			t.Fatalf("prefix %q is not the key's first sixteen characters", minted.Prefix)
		}
		if seen[minted.Key] {
			t.Fatal("the same key was minted twice")
		}
		seen[minted.Key] = true
	}
}

func TestACreateIsValidated(t *testing.T) {
	read := func(body string) (Requested, []string) {
		t.Helper()
		requested, failures, err := ReadCreate("application/json", []byte(body))
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		var messages []string
		for _, failure := range failures {
			messages = append(messages, strings.Join(failure.Location, ".")+": "+failure.Message)
		}
		return requested, messages
	}

	requested, failures := read(`{"label": "  My laptop  "}`)
	if len(failures) > 0 || requested.Label != "My laptop" || requested.ExpiresInDays != nil {
		t.Errorf("a name alone: %+v %v", requested, failures)
	}
	requested, failures = read(`{"label": "CI", "expires_in_days": null}`)
	if len(failures) > 0 || requested.ExpiresInDays != nil {
		t.Errorf("an explicit null: %+v %v", requested, failures)
	}
	requested, failures = read(`{"label": "CI", "expires_in_days": 30}`)
	if len(failures) > 0 || requested.ExpiresInDays == nil || *requested.ExpiresInDays != 30 {
		t.Errorf("thirty days: %+v %v", requested, failures)
	}
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	if at := requested.ExpiresAt(now); at == nil || !at.Equal(now.AddDate(0, 0, 30)) {
		t.Errorf("thirty days from %s is %v", now, at)
	}

	for body, want := range map[string]string{
		`{}`:               "body.label: Field required",
		`{"label": ""}`:    "body.label: String should have at least 1 character",
		`{"label": "   "}`: "body.label: " + labelBlank,
		`{"label": 7}`:     "body.label: Input should be a valid string",
		`{"label": "` + strings.Repeat("x", 121) + `"}`:            "body.label: String should have at most 120 characters",
		`{"label": "CI", "expires_in_days": 0}`:                    "body.expires_in_days: " + expiryNotPositive,
		`{"label": "CI", "expires_in_days": -3}`:                   "body.expires_in_days: " + expiryNotPositive,
		`{"label": "CI", "expires_in_days": 36501}`:                "body.expires_in_days: " + expiryTooLong,
		`{"label": "CI", "expires_in_days": 99999999999999999999}`: "body.expires_in_days: " + expiryTooLong,
		`{"label": "CI", "expires_in_days": 1.5}`:                  "body.expires_in_days: Input should be a valid integer, got a number with a fractional part",
	} {
		if _, failures := read(body); len(failures) != 1 || failures[0] != want {
			t.Errorf("%s: failures %v, want [%s]", body, failures, want)
		}
	}
	// Both fields' faults at once.
	if _, failures := read(`{"label": "", "expires_in_days": 0}`); len(failures) != 2 {
		t.Errorf("two faults answered %v", failures)
	}
}
