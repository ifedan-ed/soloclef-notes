package automationmail

import (
	"context"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

const databaseVariable = "SOLOCLEF_API_TEST_AUTOMATIONMAIL_DATABASE"

// The outbox against a real Postgres: a waiting run is taken once, with
// its own owner's address; only a run that was taken can be settled or
// put back; and a run taken long ago and never settled is given up on.
func TestTheOutboxOverARealDatabase(t *testing.T) {
	pool := testpostgres.Open(t, databaseVariable)
	const alice, bob = testpostgres.Alice, testpostgres.Bob
	ctx := context.Background()
	one := func(query string, args ...any) (id int64) {
		t.Helper()
		if err := pool.QueryRow(ctx, query, args...).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	var aliceEmail, bobEmail string
	if err := pool.QueryRow(ctx, `SELECT (SELECT email FROM "user" WHERE id = $1), (SELECT email FROM "user" WHERE id = $2)`, alice, bob).Scan(&aliceEmail, &bobEmail); err != nil {
		t.Fatal(err)
	}
	automation := func(owner, name string) int64 {
		return one(`INSERT INTO automations (user_id, name, definition) VALUES ($1, $2, '{}') RETURNING id`, owner, name)
	}
	run := func(automationID int64, status, output string) int64 {
		return one(`INSERT INTO automation_runs (automation_id, status, definition_snapshot, output, error)
			VALUES ($1, $2::automation_run_status, '{}', $3::jsonb, CASE WHEN $2 = 'failed' THEN '{"message":"It broke."}'::jsonb END) RETURNING id`, automationID, status, output)
	}
	alices, bobs := automation(alice, "Alice's digest"), automation(bob, "Bob's digest")
	first := run(alices, "succeeded", `{"final_message":"Tides.","email":{"status":"pending"}}`)
	second := run(bobs, "failed", `{"final_message":"","email":{"status":"pending","attempts":2}}`)
	// Runs the sender must leave alone: one that asked for no mail, one
	// already sent, and one with no output at all.
	run(alices, "succeeded", `{"final_message":"No mail."}`)
	sent := run(alices, "succeeded", `{"final_message":"Old.","email":{"status":"sent","to":"x@example.org"}}`)
	one(`INSERT INTO automation_runs (automation_id, status, definition_snapshot) VALUES ($1, 'running', '{}') RETURNING id`, alices)

	store := Store{Pool: pool}
	status := func(runID int64) (status, to, why string) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT COALESCE(output->'email'->>'status',''), COALESCE(output->'email'->>'to',''), COALESCE(output->'email'->>'reason','')
			FROM automation_runs WHERE id = $1`, runID).Scan(&status, &to, &why); err != nil {
			t.Fatal(err)
		}
		return status, to, why
	}

	taken, err := store.Claim(ctx, 10)
	if err != nil || len(taken) != 2 {
		t.Fatalf("claimed %+v, %v; want the two waiting runs", taken, err)
	}
	// Oldest first is not promised by the UPDATE's RETURNING, so find each.
	byRun := map[int64]Mail{}
	for _, mail := range taken {
		byRun[mail.RunID] = mail
	}
	if got := byRun[first]; got != (Mail{RunID: first, AutomationID: alices, Automation: "Alice's digest", To: aliceEmail, Active: true, Status: "succeeded", Result: "Tides."}) {
		t.Errorf("Alice's run was claimed as %+v", got)
	}
	if got := byRun[second]; got != (Mail{RunID: second, AutomationID: bobs, Automation: "Bob's digest", To: bobEmail, Active: true, Status: "failed", Failure: "It broke.", Attempts: 2}) {
		t.Errorf("Bob's run was claimed as %+v", got)
	}
	// Taken is taken: a second look, or another replica's, finds nothing.
	if again, err := store.Claim(ctx, 10); err != nil || len(again) != 0 {
		t.Errorf("a second claim took %+v, %v", again, err)
	}
	if got, _, _ := status(first); got != Sending {
		t.Errorf("a claimed run's status is %q", got)
	}

	if err := store.Settle(ctx, first, Outcome{Status: Sent, To: &aliceEmail}); err != nil {
		t.Fatal(err)
	}
	if got, to, _ := status(first); got != Sent || to != aliceEmail {
		t.Errorf("after settling: %s to %s", got, to)
	}
	// Settling again, or settling a run that was never taken, changes
	// nothing: the outcome written once stands.
	why := "late"
	if err := store.Settle(ctx, first, Outcome{Status: NotSent, Reason: &why}); err != nil {
		t.Fatal(err)
	}
	if err := store.Settle(ctx, sent, Outcome{Status: NotSent, Reason: &why}); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := status(first); got != Sent {
		t.Errorf("a settled run was settled again as %s", got)
	}
	if got, to, _ := status(sent); got != Sent || to != "x@example.org" {
		t.Errorf("a run never taken was settled: %s to %s", got, to)
	}

	// Put back, Bob's run waits again with its attempts counted, and is
	// taken again with them.
	if err := store.Release(ctx, second, 3); err != nil {
		t.Fatal(err)
	}
	retaken, err := store.Claim(ctx, 10)
	if err != nil || len(retaken) != 1 || retaken[0].RunID != second || retaken[0].Attempts != 3 {
		t.Fatalf("after a release: %+v, %v", retaken, err)
	}

	// Taken and never settled: given up on only once it is old enough.
	gone := "The server stopped while sending it."
	if count, err := store.Abandon(ctx, time.Now().Add(-time.Hour), Outcome{Status: NotSent, Reason: &gone}); err != nil || count != 0 {
		t.Errorf("abandoning runs taken a moment ago: %d, %v", count, err)
	}
	if count, err := store.Abandon(ctx, time.Now().Add(time.Hour), Outcome{Status: NotSent, Reason: &gone}); err != nil || count != 1 {
		t.Errorf("abandoning runs taken long ago: %d, %v", count, err)
	}
	if got, _, reason := status(second); got != NotSent || reason != gone {
		t.Errorf("an abandoned run: %s, %q", got, reason)
	}

	// The look has an index that holds only the waiting runs: the two
	// statuses the sender asks for are the two the index is built on.
	var predicate string
	if err := pool.QueryRow(ctx, `SELECT pg_get_expr(indpred, indrelid) FROM pg_index WHERE indexrelid = 'ix_automation_runs_email_waiting'::regclass`).Scan(&predicate); err != nil ||
		!strings.Contains(predicate, "'"+Pending+"'") || !strings.Contains(predicate, "'"+Sending+"'") {
		t.Errorf("the outbox's index holds %q (%v)", predicate, err)
	}
}
