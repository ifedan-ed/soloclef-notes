package dedupe_test

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe"
	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe/dedupetest"
)

const mailID = "aaaabbbbccccddddeeeeffff00001111"

func TestOneSenderMayClaimAnAttemptAndASecondIsRefused(t *testing.T) {
	guard := dedupe.HandoverGuard{Keys: dedupetest.NewMemory()}
	ctx := context.Background()

	if held, err := guard.Claim(ctx, mailID, 0); err != nil || held != "" {
		t.Fatalf("the first sender was refused: %q %v", held, err)
	}
	if held, _ := guard.Claim(ctx, mailID, 0); held != dedupe.Claimed {
		t.Errorf("the second sender was told %q", held)
	}
}

func TestASentMessageIsRefusedAndSaysSo(t *testing.T) {
	guard := dedupe.HandoverGuard{Keys: dedupetest.NewMemory()}
	ctx := context.Background()
	if _, err := guard.Claim(ctx, mailID, 0); err != nil {
		t.Fatal(err)
	}
	if err := guard.MarkSent(ctx, mailID, 0); err != nil {
		t.Fatal(err)
	}
	if held, _ := guard.Claim(ctx, mailID, 0); held != dedupe.Sent {
		t.Errorf("got %q", held)
	}
	if held, _ := guard.Claim(ctx, mailID, 1); held != "" {
		t.Errorf("the next attempt was refused: %q", held)
	}
}

func TestAStaleClaimIsReleasedButASentMarkNever(t *testing.T) {
	guard := dedupe.HandoverGuard{Keys: dedupetest.NewMemory()}
	ctx := context.Background()

	if _, err := guard.Claim(ctx, mailID, 0); err != nil {
		t.Fatal(err)
	}
	if err := guard.Release(ctx, mailID, 0); err != nil {
		t.Fatal(err)
	}
	if held, _ := guard.Claim(ctx, mailID, 0); held != "" {
		t.Errorf("a dead sender's claim outlived it: %q", held)
	}
	if err := guard.MarkSent(ctx, mailID, 0); err != nil {
		t.Fatal(err)
	}
	if err := guard.Release(ctx, mailID, 0); err != nil {
		t.Fatal(err)
	}
	if held, _ := guard.Claim(ctx, mailID, 0); held != dedupe.Sent {
		t.Errorf("recovery forgot that the mail server took it: %q", held)
	}
}

func TestAfterTheWindowARecoveredMessageIsSentAgain(t *testing.T) {
	keys := dedupetest.NewMemory()
	guard := dedupe.HandoverGuard{Keys: keys}
	ctx := context.Background()
	if _, err := guard.Claim(ctx, mailID, 0); err != nil {
		t.Fatal(err)
	}
	if err := guard.MarkSent(ctx, mailID, 0); err != nil {
		t.Fatal(err)
	}
	keys.Advance(dedupe.Window + 1)
	if held, _ := guard.Claim(ctx, mailID, 0); held != "" {
		t.Errorf("got %q", held)
	}
}
