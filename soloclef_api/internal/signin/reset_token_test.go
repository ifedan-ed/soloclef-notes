package signin

import (
	"strings"
	"testing"
	"time"
)

// A reset token is tied to the password in place when it was minted, and
// says nothing about that password to whoever reads the mail.
func TestTheResetTokenCarriesAKeyedFingerprintOfThePassword(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	stored := "$argon2id$v=19$m=65536,t=3,p=4$c2FsdHNhbHRzYWx0c2FsdA$a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2V5a2U"
	token, err := mintResetToken("one-secret", "11111111-1111-4111-8111-111111111111", &stored, now)
	if err != nil {
		t.Fatal(err)
	}
	userID, fingerprint, ok := readResetToken("one-secret", token, now.Add(59*time.Minute))
	if !ok || userID != "11111111-1111-4111-8111-111111111111" || fingerprint != passwordFingerprint("one-secret", &stored) {
		t.Fatalf("read %q %q %v", userID, fingerprint, ok)
	}
	if strings.Contains(token, "argon2id") || strings.Contains(fingerprint, "c2FsdHNhbHRz") {
		t.Error("the token carries the stored hash")
	}
	// Another password, no password and another secret are three other
	// fingerprints.
	other := stored + "x"
	seen := map[string]bool{fingerprint: true}
	for _, print := range []string{
		passwordFingerprint("one-secret", &other),
		passwordFingerprint("one-secret", nil),
		passwordFingerprint("another-secret", &stored),
	} {
		if seen[print] {
			t.Errorf("two different passwords or secrets share the fingerprint %s", print)
		}
		seen[print] = true
	}
	if _, _, ok := readResetToken("one-secret", token, now.Add(time.Hour+time.Second)); ok {
		t.Error("the token outlived its hour")
	}
	if _, _, ok := readResetToken("another-secret", token, now); ok {
		t.Error("the token verified under another secret")
	}
}

func TestOnlyABareMailAddressIsUsable(t *testing.T) {
	for raw, want := range map[string]string{
		" person@example.org ":         "person@example.org",
		"Person.Name+tag@Example.Org":  "Person.Name+tag@Example.Org",
		"":                             "",
		"person":                       "",
		"person@":                      "",
		"person@localhost":             "",
		"Person <person@example.org>":  "",
		"a@example.org, b@example.org": "",
		strings.Repeat("a", 320) + "@example.org": "",
	} {
		got, ok := usableAddress(raw)
		if got != want || ok != (want != "") {
			t.Errorf("%q read as %q %v, want %q", raw, got, ok, want)
		}
	}
}
