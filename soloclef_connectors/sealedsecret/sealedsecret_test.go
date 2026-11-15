package sealedsecret

import (
	"errors"
	"testing"
)

func mustNew(t *testing.T, secret string, purpose Purpose) Seal {
	t.Helper()
	seal, err := New(secret, purpose)
	if err != nil {
		t.Fatal(err)
	}
	return seal
}

func TestASealedValueOpensForItsOwnerAndPurposeOnly(t *testing.T) {
	seal := mustNew(t, "deployment-secret", MCPServerSecret)
	sealed, err := seal.Seal("owner-a", "sk-live-1234")
	if err != nil {
		t.Fatal(err)
	}
	if !IsSealed(sealed) || sealed == "sk-live-1234" {
		t.Fatalf("sealed = %q", sealed)
	}
	if got, err := seal.Open("owner-a", sealed); err != nil || got != "sk-live-1234" {
		t.Fatalf("open = %q, %v", got, err)
	}
	for name, open := range map[string]func() (string, error){
		"another owner":   func() (string, error) { return seal.Open("owner-b", sealed) },
		"another purpose": func() (string, error) { return mustNew(t, "deployment-secret", WebSearchKey).Open("owner-a", sealed) },
		"another secret":  func() (string, error) { return mustNew(t, "other-secret", MCPServerSecret).Open("owner-a", sealed) },
		"plain text":      func() (string, error) { return seal.Open("owner-a", "sk-live-1234") },
		"damaged":         func() (string, error) { return seal.Open("owner-a", sealed[:len(sealed)-2]+"AA") },
		"not base64":      func() (string, error) { return seal.Open("owner-a", "v1:***") },
		"too short":       func() (string, error) { return seal.Open("owner-a", "v1:AAAA") },
	} {
		if _, err := open(); !errors.Is(err, ErrUnreadable) {
			t.Errorf("%s: err = %v, want ErrUnreadable", name, err)
		}
	}
}

func TestSealingTwiceStoresTwoTexts(t *testing.T) {
	seal := mustNew(t, "s", WebSearchKey)
	first, _ := seal.Seal("o", "same")
	second, _ := seal.Seal("o", "same")
	if first == second {
		t.Fatal("the nonce was reused")
	}
}

// The model connection label is the one the existing rows were sealed
// under before this package existed; changing it would lose every key.
func TestTheModelConnectionLabelIsTheOneRowsWereSealedUnder(t *testing.T) {
	if ModelConnectionKey != "soloclef model connection key v1" {
		t.Fatal("the model connection label changed: every stored model key would stop opening")
	}
}

func TestAZeroOrUnsetSealRefuses(t *testing.T) {
	if _, err := New("", MCPServerSecret); err == nil {
		t.Error("an empty secret was accepted")
	}
	if _, err := New("s", ""); err == nil {
		t.Error("an empty purpose was accepted")
	}
	var zero Seal
	if zero.Ready() {
		t.Error("the zero seal says it is ready")
	}
	if _, err := zero.Seal("o", "x"); err == nil {
		t.Error("the zero seal sealed")
	}
}
