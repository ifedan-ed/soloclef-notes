package maildrafts

import (
	"context"
	"testing"
	"time"
)

// The Drafts waiting list over a real Postgres (skipped without one; see
// databaseVariable): which drafts it holds, in which order, and whose.
func TestTheWaitingListOverARealDatabase(t *testing.T) {
	f := openGuard(t)
	guard, clock := f.guard, f.clock
	ctx := context.Background()

	create := func(owner, account, subject string) string {
		t.Helper()
		message := draftFor(account)
		message.Subject = subject
		created, err := guard.Create(ctx, owner, ProviderJMAP, message, "")
		if err != nil {
			t.Fatal(err)
		}
		// Each later than the last, so "newest first" has an order to keep.
		clock.advance(time.Second)
		return created.ID
	}
	older := create(ownerA, accountA, "Older")
	discarded := create(ownerA, accountA, "Discarded")
	newer := create(ownerA, accountA, "Newer")
	create(ownerB, accountB, "Ben's own")

	if _, err := guard.Discard(ctx, ownerA, discarded); err != nil {
		t.Fatal(err)
	}

	list, err := guard.Waiting(ctx, ownerA)
	if err != nil {
		t.Fatal(err)
	}
	if list.More || len(list.Drafts) != 2 || list.Drafts[0].ID != newer || list.Drafts[1].ID != older {
		t.Fatalf("want Newer then Older, got %+v", list)
	}
	first := list.Drafts[0]
	if first.Subject != "Newer" || first.AccountLabel != "Ada's Stalwart" || first.Status != StatusPending || !first.ExpiresAt.After(first.CreatedAt) {
		t.Fatalf("the line names the draft wrongly: %+v", first)
	}

	// A draft that has run out is no longer waiting, though its row stays.
	clock.advance(DraftLifetime)
	list, err = guard.Waiting(ctx, ownerA)
	if err != nil || len(list.Drafts) != 0 {
		t.Fatalf("expired drafts are listed: %+v, %v", list, err)
	}

	// An account with none gets an empty list, never null.
	none, err := guard.Waiting(ctx, "cccccccc-0000-4000-8000-000000000003")
	if err != nil || none.Drafts == nil || len(none.Drafts) != 0 {
		t.Fatalf("an empty account: %+v, %v", none, err)
	}
}

// More than MostWaitingListed are cut to the newest, and said to be more.
func TestTheWaitingListSaysWhenThereAreMore(t *testing.T) {
	f := openGuard(t)
	ctx := context.Background()
	for range MostWaitingListed + 2 {
		if _, err := f.guard.Create(ctx, ownerA, ProviderJMAP, draftFor(accountA), ""); err != nil {
			t.Fatal(err)
		}
		f.clock.advance(time.Millisecond)
	}
	list, err := f.guard.Waiting(ctx, ownerA)
	if err != nil || !list.More || len(list.Drafts) != MostWaitingListed {
		t.Fatalf("got %d drafts, more=%v, %v", len(list.Drafts), list.More, err)
	}
}
