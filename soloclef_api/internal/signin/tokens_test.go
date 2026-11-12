package signin

import (
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

// The access token this package mints is the one the gate accepts: the
// contract between the two halves of a session, checked end to end. It names
// the sign-in as well as the account, which is what lets the gate end it when
// the sign-in ends (revocation_database_test.go).
func TestTheGateAcceptsTheAccessTokenASignInMints(t *testing.T) {
	const secret = "a-test-secret"
	const account, family = "11111111-1111-4111-8111-111111111111", "0b9a6f3e-5d2c-4e8f-9a1b-2c3d4e5f6a7b"
	now := time.Now()
	token, expires, err := mintAccess(secret, account, family, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := now.Add(time.Hour).Unix(); expires != want {
		t.Errorf("expires %d, want %d", expires, want)
	}
	session, err := principal.SessionToken{Secret: secret, MinIssuedAt: now.Add(-time.Minute).Unix()}.Read(token)
	if err != nil || session.UserID != account || session.SessionID != family {
		t.Fatalf("the gate read %+v, %v", session, err)
	}
	if _, err := (principal.SessionToken{Secret: "another-secret"}).Read(token); err == nil {
		t.Fatal("a token verified with a secret it was not signed with")
	}
	if got, ok := accessExpiry(secret, token); !ok || got != expires {
		t.Errorf("accessExpiry = %d, %v", got, ok)
	}
}

func TestARefreshTokenIsRandomAndStoredOnlyAsItsHash(t *testing.T) {
	a, _ := newRefreshToken()
	b, _ := newRefreshToken()
	if a == b || len(a) != 43 {
		t.Fatalf("tokens %q and %q", a, b)
	}
	if h := hashToken(a); len(h) != 64 || h == a {
		t.Fatalf("hash %q", h)
	}
	family, _ := newFamilyID()
	if len(family) != 36 || family[14] != '4' {
		t.Fatalf("family id %q is not a version 4 uuid", family)
	}
}

func TestHowLongEachKindOfRefreshTokenLives(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name string
		kind string
		cap  time.Time
		want time.Time
	}{
		{"a browser's slides by its idle time", kindWeb, now.Add(webAbsolute), now.Add(webIdle)},
		{"a browser's never passes its sign-in's cap", kindWeb, now.Add(time.Hour), now.Add(time.Hour)},
		{"a phone's lives as long as its sign-in", kindPhone, now.Add(phoneLifetime), now.Add(phoneLifetime)},
	} {
		if got := tokenExpiry(c.kind, now, c.cap); !got.Equal(c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
	if familyLifetime(kindPhone) != 365*24*time.Hour || familyLifetime(kindWeb) != 30*24*time.Hour {
		t.Error("the sign-in lifetimes changed")
	}
}

func TestAPhonesNameIsTidiedAndBounded(t *testing.T) {
	name := func(s string) *string { return &s }
	if got := deviceName(name("  iPhone 15   Pro\t· Soloclef app ")); got == nil || *got != "iPhone 15 Pro · Soloclef app" {
		t.Errorf("got %v", got)
	}
	if deviceName(name("   ")) != nil || deviceName(nil) != nil {
		t.Error("a blank name named a device")
	}
	long := deviceName(name(strings.Repeat("é", 150)))
	if long == nil || len([]rune(*long)) != 100 {
		t.Errorf("a long name kept %d characters", len([]rune(*long)))
	}
}
