package signin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

func TestTheOperatorMakesAnAccountThatThenSignsIn(t *testing.T) {
	h, count := newHarness(t)
	id, created, err := SetPassword(context.Background(), h.pool, NewHasher(1), " Carol@Example.test ", "a new passphrase", h.clock)
	if err != nil || !created || id == "" {
		t.Fatalf("id=%q created=%v err=%v", id, created, err)
	}
	if userOf(t, pairOf(t, h.phoneSignIn("carol@example.test", "a new passphrase", "phone")).AccessToken) != id {
		t.Fatal("the new account's sign-in names another account")
	}
	if n := count(`SELECT count(*) FROM "user" WHERE id = $1 AND is_verified`, id); n != 1 {
		t.Error("an operator-made account is not verified")
	}
}

// A new password ends every sign-in of that account and of no other.
func TestANewPasswordEndsEverySignInOfThatAccountOnly(t *testing.T) {
	h, _ := newHarness(t)
	hers := pairOf(t, h.phoneSignIn("alice@example.test", alicePassword, "phone"))
	his := pairOf(t, h.phoneSignIn("bob@example.test", bobPassword, "phone"))
	id, created, err := SetPassword(context.Background(), h.pool, NewHasher(1), "ALICE@example.test", "her new passphrase", h.clock)
	if err != nil || created || id != testpostgres.Alice {
		t.Fatalf("id=%q created=%v err=%v", id, created, err)
	}
	if code := h.bodyRefresh(RefreshPath, *hers.RefreshToken).Code; code != http.StatusUnauthorized {
		t.Errorf("her old sign-in still refreshes: %d", code)
	}
	pairOf(t, h.bodyRefresh(RefreshPath, *his.RefreshToken))
	if code := h.webSignIn("alice@example.test", alicePassword).Code; code != http.StatusBadRequest {
		t.Errorf("her old password still signs in: %d", code)
	}
	pairOf(t, h.phoneSignIn("alice@example.test", "her new passphrase", "phone"))
}

func TestAPasswordThatCannotBeUsedIsRefusedBeforeAnythingIsWritten(t *testing.T) {
	for _, c := range []struct{ address, password string }{
		{"dave@example.test", "short"},
		{"dave@example.test", strings.Repeat("x", 257)},
		{"dave@example.test", "DAVE@example.test"},
	} {
		if err := checkNewPassword(c.address, c.password); !errors.Is(err, ErrWeakPassword) {
			t.Errorf("%q: %v", c.password, err)
		}
	}
	if err := checkNewPassword("dave@example.test", "eight ch"); err != nil {
		t.Errorf("an eight-character password: %v", err)
	}
}

// The operator names an administrator by address, in any case, and only
// that account's flag changes.
func TestTheOperatorMakesOneAccountAnAdministratorAndNoOther(t *testing.T) {
	h, count := newHarness(t)
	ctx := context.Background()
	id, err := SetAdministrator(ctx, h.pool, " ALICE@example.test ", true)
	if err != nil || id != testpostgres.Alice {
		t.Fatalf("id=%q err=%v", id, err)
	}
	if n := count(`SELECT count(*) FROM "user" WHERE is_superuser`); n != 1 {
		t.Fatalf("%d administrators, want Alice alone", n)
	}
	if n := count(`SELECT count(*) FROM "user" WHERE id = $1 AND is_superuser`, testpostgres.Bob); n != 0 {
		t.Error("Bob became an administrator with her")
	}
	if _, err := SetAdministrator(ctx, h.pool, "alice@example.test", false); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM "user" WHERE is_superuser`); n != 0 {
		t.Errorf("%d administrators after hers was taken back", n)
	}
	if _, err := SetAdministrator(ctx, h.pool, "nobody@example.test", true); !errors.Is(err, ErrNoSuchAccount) {
		t.Errorf("an address with no account: %v", err)
	}
}
