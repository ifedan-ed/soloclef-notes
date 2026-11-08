package dedupe_test

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe"
	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe/dedupetest"
)

const callerKey = "password_reset:abc"

func TestTheFirstPushTakesTheKeyAndTheSecondIsToldWhoHasIt(t *testing.T) {
	ledger := dedupe.QueuedOnce{Keys: dedupetest.NewMemory()}
	ctx := context.Background()

	held, err := ledger.Claim(ctx, callerKey, "mail-1")
	if err != nil || held != "" {
		t.Fatalf("the first push was refused: %q %v", held, err)
	}
	held, err = ledger.Claim(ctx, callerKey, "mail-2")
	if err != nil || held != "mail-1" {
		t.Fatalf("the second push was told %q %v", held, err)
	}
}

func TestADifferentKeyIsADifferentMail(t *testing.T) {
	ledger := dedupe.QueuedOnce{Keys: dedupetest.NewMemory()}
	ctx := context.Background()
	if _, err := ledger.Claim(ctx, callerKey, "mail-1"); err != nil {
		t.Fatal(err)
	}
	if held, err := ledger.Claim(ctx, "password_reset:def", "mail-2"); err != nil || held != "" {
		t.Fatalf("got %q %v", held, err)
	}
}

func TestOnceTheWindowPassesTheSameMailMayBeQueuedAgain(t *testing.T) {
	keys := dedupetest.NewMemory()
	ledger := dedupe.QueuedOnce{Keys: keys}
	ctx := context.Background()
	if _, err := ledger.Claim(ctx, callerKey, "mail-1"); err != nil {
		t.Fatal(err)
	}
	keys.Advance(dedupe.Window + 1)
	if held, err := ledger.Claim(ctx, callerKey, "mail-2"); err != nil || held != "" {
		t.Fatalf("got %q %v", held, err)
	}
}

func TestAPushThatFailedGivesTheKeyBackButNeverAnotherMails(t *testing.T) {
	ledger := dedupe.QueuedOnce{Keys: dedupetest.NewMemory()}
	ctx := context.Background()
	if _, err := ledger.Claim(ctx, callerKey, "mail-1"); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Release(ctx, callerKey, "mail-2"); err != nil {
		t.Fatal(err)
	}
	if held, _ := ledger.Claim(ctx, callerKey, "mail-3"); held != "mail-1" {
		t.Errorf("mail-2 released a key it never held; mail-3 was told %q", held)
	}
	if err := ledger.Release(ctx, callerKey, "mail-1"); err != nil {
		t.Fatal(err)
	}
	if held, _ := ledger.Claim(ctx, callerKey, "mail-4"); held != "" {
		t.Errorf("a failed push kept its identity: %q", held)
	}
}
