package studiogenerate

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/testpostgres"
)

// Only the attempt a job is running now keeps its conversation: an
// earlier attempt's, an ended job's and a deleted job's are removed by the
// sweep, and nothing that is not a Studio run's conversation is touched.
func TestTheSweepForgetsTheConversationsOfEndedJobs(t *testing.T) {
	h := newBareHarness(t, false)
	ctx := context.Background()
	// Conversations are named, not owned, so emptying the accounts does
	// not empty them: this test starts from none.
	testpostgres.Exec(t, h.pool, `DELETE FROM soloclef_agent_checkpoints`)
	running, _ := h.seedJob("running", 2, 0)
	stopping, _ := h.seedJob("cancelling", 1, 0)
	failed, _ := h.seedJob("failed", 3, 0)
	queued, _ := h.seedJob("queued", 2, 0)
	cancelled, _ := h.seedJob("cancelled", 1, 0)
	name := func(job, attempt int64) string { return attemptConversation(job, attempt) }
	kept := []string{
		name(running, 2), name(running, 2) + "/call_a", name(stopping, 1),
		// Not a Studio run's conversation, though it begins like one.
		"chat-" + strconv.FormatInt(running, 10), "studio-job-notes", "studio-job-" + strconv.FormatInt(running, 10) + "-attempt-x",
	}
	gone := []string{
		name(running, 1), name(running, 1) + "/call_a", name(failed, 1), name(failed, 3), name(queued, 1),
		name(cancelled, 1), name(987654321, 1),
	}
	for _, thread := range append(append([]string{}, kept...), gone...) {
		h.exec(`INSERT INTO soloclef_agent_checkpoints (thread_id, seq, status, node, state) VALUES ($1, 1, 'done', '', '\x7b7d')`, thread)
	}

	// The runner sweeps as it starts.
	h.runnerWith(h.lifetime, h.lifetime)
	h.until("the sweep has run", "(SELECT 1 FROM soloclef_agent_checkpoints HAVING count(*) = "+strconv.Itoa(len(kept))+") AS swept")
	var left []string
	rows, err := h.pool.Query(ctx, `SELECT thread_id FROM soloclef_agent_checkpoints ORDER BY thread_id COLLATE "C"`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var thread string
		if err := rows.Scan(&thread); err != nil {
			t.Fatal(err)
		}
		left = append(left, thread)
	}
	for _, thread := range kept {
		if !strings.Contains(" "+strings.Join(left, " ")+" ", " "+thread+" ") {
			t.Errorf("%s was removed; left %v", thread, left)
		}
	}

	// A batch bounds one sweep's statement.
	for i := int64(0); i < 3; i++ {
		h.exec(`INSERT INTO soloclef_agent_checkpoints (thread_id, seq, status, node, state) VALUES ($1, 1, 'done', '', '\x7b7d')`, name(failed, 10+i))
	}
	removed, err := New(h.pool).ForgetEndedConversations(ctx, 2)
	if err != nil || removed != 2 {
		t.Errorf("a sweep bounded at two removed %d, %v", removed, err)
	}
}
