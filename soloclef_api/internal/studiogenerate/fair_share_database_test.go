package studiogenerate

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

// These tests are how the Studio's places are shared between accounts
// (fair_share.go), against the throwaway Postgres
// (SOLOCLEF_API_TEST_STUDIOGENERATE_DATABASE).

// queueFor writes a queued artifact job for an account's notebook, made at
// the given moment, and answers its id.
func (h *harness) queueFor(userID string, notebookID int64, at time.Time) int64 {
	h.t.Helper()
	ctx := context.Background()
	q := New(h.pool)
	threadID, err := q.CreateStudioThread(ctx, CreateStudioThreadParams{Title: "Queued", NotebookID: notebookID, UserID: userID})
	if err != nil {
		h.t.Fatal(err)
	}
	stored, err := encodeTask("mindmap", "mindmap", Job{Task: "Kind of artifact: mindmap.\n\n## Source: Moon\n\nThe moon pulls the sea."})
	if err != nil {
		h.t.Fatal(err)
	}
	var id int64
	if err := h.pool.QueryRow(ctx, `
		INSERT INTO deliverable_jobs (user_id, kind, title, thread_id, tool_call_id, request, status, phase, created_at, updated_at)
		VALUES ($1, 'artifact', 'Mind Map', $2, $3, $4, 'queued', 'queued', $5, $5) RETURNING id`,
		userID, threadID, "queued-"+strconv.FormatInt(threadID, 10), stored, at).Scan(&id); err != nil {
		h.t.Fatal(err)
	}
	return id
}

// exec runs one statement with arguments, failing the test on an error.
func (h *harness) exec(statement string, arguments ...any) {
	h.t.Helper()
	if _, err := h.pool.Exec(context.Background(), statement, arguments...); err != nil {
		h.t.Fatal(err)
	}
}

func claimedIDs(rows []ClaimQueuedJobsRow) []int64 {
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	slices.Sort(ids)
	return ids
}

// The places go round the accounts, each account's oldest waiting job
// first, and no account has more than its share running.
func TestTheClaimGoesRoundTheAccounts(t *testing.T) {
	h := newBareHarness(t, false)
	ctx := context.Background()
	start := time.Now().UTC().Add(-time.Hour)
	a1 := h.queueFor(alice, 10, start)
	a2 := h.queueFor(alice, 10, start.Add(time.Minute))
	a3 := h.queueFor(alice, 10, start.Add(2*time.Minute))
	b1 := h.queueFor(bob, 20, start.Add(3*time.Minute))
	b2 := h.queueFor(bob, 20, start.Add(4*time.Minute))

	// Three places: each account's first, then Alice's second (her first
	// has waited longest).
	rows, err := claimFairly(ctx, h.pool, time.Now().UTC(), runsPerAccount, 3)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := claimedIDs(rows), []int64{a1, a2, b1}; !slices.Equal(got, want) {
		t.Fatalf("the first claim took %v, want %v", got, want)
	}
	// Many places: Alice is at her share, so only Bob's second starts.
	rows, err = claimFairly(ctx, h.pool, time.Now().UTC(), runsPerAccount, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := claimedIDs(rows), []int64{b2}; !slices.Equal(got, want) {
		t.Fatalf("the second claim took %v, want %v", got, want)
	}
	// A job being stopped still holds its place; one that ended frees it.
	h.exec(`UPDATE deliverable_jobs SET status = 'cancelling' WHERE id = $1`, a1)
	if rows, err := claimFairly(ctx, h.pool, time.Now().UTC(), runsPerAccount, 10); err != nil || len(rows) != 0 {
		t.Fatalf("a claim while Alice's stopping job holds its place took %v, %v", claimedIDs(rows), err)
	}
	h.exec(`UPDATE deliverable_jobs SET status = 'cancelled' WHERE id = $1`, a1)
	rows, err = claimFairly(ctx, h.pool, time.Now().UTC(), runsPerAccount, 10)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := claimedIDs(rows), []int64{a3}; !slices.Equal(got, want) {
		t.Fatalf("the claim after Alice's job ended took %v, want %v", got, want)
	}
}

// Replicas claiming at once never start more than an account's share
// between them: the share is counted in the database, one claim at a time.
func TestReplicasClaimingAtOnceKeepTheShare(t *testing.T) {
	h := newBareHarness(t, false)
	start := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 20; i++ {
		h.queueFor(alice, 10, start.Add(time.Duration(i)*time.Second))
	}
	var wait sync.WaitGroup
	for replica := 0; replica < 4; replica++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 5 {
				if _, err := claimFairly(context.Background(), h.pool, time.Now().UTC(), runsPerAccount, 4); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wait.Wait()
	if running := h.count("deliverable_jobs WHERE status = 'running'"); running != runsPerAccount {
		t.Fatalf("%d of one account's jobs running, want its share of %d", running, runsPerAccount)
	}
}

// One account flooding the queue does not hold back another's job: it
// starts at once, beside the flooder's share, in a process with places to
// spare.
func TestAFloodingAccountDoesNotHoldBackAnother(t *testing.T) {
	h := newBareHarness(t, false)
	hold := make(chan struct{})
	h.model.hold = hold
	start := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 8; i++ {
		h.queueFor(alice, 10, start.Add(time.Duration(i)*time.Second))
	}
	h.runnerOfSize(h.lifetime, h.lifetime, 4)
	h.until("Alice's share is running", "(SELECT 1 FROM deliverable_jobs WHERE status = 'running' AND user_id = '"+alice+"' HAVING count(*) = 2) AS share")

	status, body := h.as(bob, "POST", "/api/v1/notebooks/20/studio/generate", `{"kind":"mindmap","request_id":"bobs-one"}`)
	if status != 201 {
		t.Fatalf("Bob's request: %d %s", status, body)
	}
	h.until("Bob's job starts", "deliverable_jobs WHERE status = 'running' AND user_id = '"+bob+"'")
	if running := h.count("deliverable_jobs WHERE status = 'running' AND user_id = '" + alice + "'"); running != runsPerAccount {
		t.Errorf("%d of Alice's jobs running beside Bob's, want her share of %d", running, runsPerAccount)
	}

	// Let them all finish: the queue still empties, two of Alice's at a time.
	close(hold)
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if h.count("(SELECT 1 FROM deliverable_jobs HAVING count(*) FILTER (WHERE status <> 'ready') = 0) AS made") > 0 {
			return
		}
	}
	// Say which jobs did not end ready, and why, rather than only that one
	// did not.
	rows, err := h.pool.Query(context.Background(), `SELECT id, user_id, status, coalesce(failure_code, ''), coalesce(left(internal_error, 200), '') FROM deliverable_jobs WHERE status <> 'ready' ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var user, status, code, cause string
		if err := rows.Scan(&id, &user, &status, &code, &cause); err != nil {
			t.Fatal(err)
		}
		t.Errorf("job %d of %s ended %s %s: %s", id, user, status, code, cause)
	}
}

// The generate route refuses an account's request once it has its share of
// jobs waiting or being made, in a sentence, and writes nothing for it;
// another account is not affected, and a job that ends frees a place.
func TestTheRouteRefusesAnAccountOverItsShare(t *testing.T) {
	h := newBareHarness(t, false)
	for i := 0; i < maxUnfinishedPerAccount; i++ {
		h.queueMindMap("full-" + strconv.Itoa(i))
	}
	threads := h.count("new_chat_threads")
	status, body := h.as(alice, "POST", generate, `{"kind":"mindmap","request_id":"one-too-many"}`)
	if status != 429 {
		t.Fatalf("the request over the share: %d %s", status, body)
	}
	var envelope struct {
		Error struct{ Message string } `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil || envelope.Error.Message != tooManyUnfinished {
		t.Errorf("the refusal says %q (%v)", envelope.Error.Message, err)
	}
	if h.count("new_chat_threads") != threads || h.count("deliverable_jobs") != maxUnfinishedPerAccount {
		t.Error("a refused request wrote a thread or a job")
	}
	if status, body := h.as(bob, "POST", "/api/v1/notebooks/20/studio/generate", `{"kind":"mindmap","request_id":"bobs"}`); status != 201 {
		t.Errorf("another account's request: %d %s", status, body)
	}
	h.exec(`UPDATE deliverable_jobs SET status = 'ready' WHERE id = (
		SELECT min(id) FROM deliverable_jobs WHERE user_id = $1)`, alice)
	if status, body := h.as(alice, "POST", generate, `{"kind":"mindmap","request_id":"after-one-ended"}`); status != 201 {
		t.Errorf("a request once a job ended: %d %s", status, body)
	}
}
