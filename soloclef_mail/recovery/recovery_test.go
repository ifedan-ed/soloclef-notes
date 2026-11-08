package recovery_test

import (
	"context"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe"
	"git.danvics.com/danvics/soloclef/soloclef_mail/dedupe/dedupetest"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailjob"
	"git.danvics.com/danvics/soloclef/soloclef_mail/recovery"
)

// fakeQueue is the three moves recovery makes, over in-memory held lists.
type fakeQueue struct {
	held  map[string][]string
	ready []string
}

func (q *fakeQueue) HeldKeys(context.Context) ([]string, error) {
	keys := make([]string, 0, len(q.held))
	for key := range q.held {
		keys = append(keys, key)
	}
	return keys, nil
}

func (q *fakeQueue) NewestHeld(_ context.Context, key string) (string, bool, error) {
	entries := q.held[key]
	if len(entries) == 0 {
		return "", false, nil
	}
	return entries[len(entries)-1], true, nil
}

func (q *fakeQueue) PutBack(_ context.Context, key string) (bool, error) {
	entries := q.held[key]
	if len(entries) == 0 {
		return false, nil
	}
	q.held[key] = entries[:len(entries)-1]
	q.ready = append(q.ready, entries[len(entries)-1])
	return true, nil
}

func aHeldJob(t *testing.T) (mailjob.Job, string) {
	t.Helper()
	job := mailjob.Job{
		Message:    mailjob.Message{To: "person@queue.test", Subject: "held", TextBody: "x"},
		ID:         "aaaabbbbccccddddeeeeffff00001111",
		Kind:       "invite",
		Ref:        "7",
		EnqueuedAt: 1762608000,
	}
	raw, err := job.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return job, raw
}

func TestEveryHeldMessageGoesBackInLine(t *testing.T) {
	_, raw := aHeldJob(t)
	queue := &fakeQueue{held: map[string][]string{"soloclef:mail:sending:0": {raw, raw}}}

	moved, err := recovery.RequeueHeld(context.Background(), queue, dedupe.HandoverGuard{Keys: dedupetest.NewMemory()})
	if err != nil || moved != 2 {
		t.Fatalf("moved %d %v", moved, err)
	}
	if len(queue.ready) != 2 {
		t.Errorf("%d back in line", len(queue.ready))
	}
}

func TestAClaimLeftByAStoppedSenderIsDropped(t *testing.T) {
	job, raw := aHeldJob(t)
	guard := dedupe.HandoverGuard{Keys: dedupetest.NewMemory()}
	ctx := context.Background()
	if _, err := guard.Claim(ctx, job.ID, job.Attempt); err != nil {
		t.Fatal(err)
	}
	queue := &fakeQueue{held: map[string][]string{"soloclef:mail:sending:0": {raw}}}

	if _, err := recovery.RequeueHeld(ctx, queue, guard); err != nil {
		t.Fatal(err)
	}

	if held, _ := guard.Claim(ctx, job.ID, job.Attempt); held != "" {
		t.Errorf("the dead sender's claim outlived it (%q), so its mail will never go out", held)
	}
}

func TestAMessageAlreadySentKeepsThatMark(t *testing.T) {
	job, raw := aHeldJob(t)
	guard := dedupe.HandoverGuard{Keys: dedupetest.NewMemory()}
	ctx := context.Background()
	if _, err := guard.Claim(ctx, job.ID, job.Attempt); err != nil {
		t.Fatal(err)
	}
	if err := guard.MarkSent(ctx, job.ID, job.Attempt); err != nil {
		t.Fatal(err)
	}
	queue := &fakeQueue{held: map[string][]string{"soloclef:mail:sending:0": {raw}}}

	if _, err := recovery.RequeueHeld(ctx, queue, guard); err != nil {
		t.Fatal(err)
	}

	if len(queue.ready) != 1 {
		t.Fatalf("%d back in line", len(queue.ready))
	}
	if held, _ := guard.Claim(ctx, job.ID, job.Attempt); held != dedupe.Sent {
		t.Errorf("recovery forgot the message had been sent: %q", held)
	}
}

func TestAnUnreadableHeldEntryIsStillPutBack(t *testing.T) {
	queue := &fakeQueue{held: map[string][]string{"soloclef:mail:sending:0": {"not json"}}}
	moved, err := recovery.RequeueHeld(context.Background(), queue, dedupe.HandoverGuard{Keys: dedupetest.NewMemory()})
	if err != nil || moved != 1 {
		t.Fatalf("moved %d %v", moved, err)
	}
}
