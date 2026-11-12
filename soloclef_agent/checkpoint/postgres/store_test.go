package postgres_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint/checkpointtest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint/postgres"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// databaseEnv names the variable holding a Postgres URL to test against. The
// test creates one table (soloclef_agent_checkpoints, if absent) and writes
// only rows whose thread id starts with a prefix unique to the run, which it
// removes afterwards. Point it at a scratch database all the same.
const databaseEnv = "SOLOCLEF_AGENT_TEST_DATABASE_URL"

// scratch connects to the test database and returns a store on it, with the
// table in place, and a thread-id prefix unique to this test whose rows are
// removed when the test ends.
func scratch(t *testing.T) (*postgres.Store, string) {
	t.Helper()
	url := os.Getenv(databaseEnv)
	if url == "" {
		// A skip reads as "ok" in a summary line. This store is only proved
		// by running these tests with the variable set.
		t.Skipf("%s is not set: the Postgres store was NOT tested", databaseEnv)
	}
	ctx := context.Background()

	// The contract includes writers racing, and a single connection cannot
	// race with itself, so the store is given a handle that opens a
	// connection per call. A real server would pass a pool.
	store, err := postgres.New(&perCall{url: url})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}
	// Running it twice must be harmless: it is what every start-up does.
	if err := store.EnsureSchema(ctx); err != nil {
		t.Fatal(err)
	}

	prefix := fmt.Sprintf("test-%d/", time.Now().UnixNano())
	t.Cleanup(func() {
		conn, err := pgx.Connect(ctx, url)
		if err != nil {
			t.Errorf("connect to clean up: %v", err)
			return
		}
		defer conn.Close(ctx)
		if _, err := conn.Exec(ctx, `DELETE FROM soloclef_agent_checkpoints WHERE thread_id LIKE $1`, prefix+"%"); err != nil {
			t.Errorf("clean up test rows: %v", err)
		}
	})
	return store, prefix
}

func TestPostgresKeepsTheStoreContract(t *testing.T) {
	store, prefix := scratch(t)
	checkpointtest.Run(t, store, prefix)

	if _, err := postgres.New(nil); err == nil {
		t.Fatal("a nil database handle was accepted")
	}
}

// The property the store exists for, end to end on a real database: a turn
// pauses for approval in one process and is resumed by another that shares
// nothing with it but the table.
func TestATurnPausedInOneProcessIsResumedByAnotherThroughPostgres(t *testing.T) {
	store, prefix := scratch(t)
	ctx := context.Background()
	thread := prefix + "chat-1"
	runs := 0
	tools := []tool.Tool{{
		Name: "delete_note", Parameters: []byte(`{"type":"object"}`), Approval: tool.AlwaysAsk,
		Run: func(_ context.Context, c tool.Call) (string, error) {
			runs++
			return "deleted " + string(c.Args), nil
		},
	}}
	process := func(replies ...modeltest.Reply) *agent.Agent {
		a, err := agent.New(agent.Config{Model: modeltest.NewScript(replies...), Tools: tools, MaxSteps: 10, MaxParallelTools: 2, Store: store})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}

	// A NUL in the arguments: the kind of byte a jsonb column would refuse,
	// in the place it would actually turn up.
	args := "{\"id\":7,\"note\":\"a\\u0000b\"}"
	first := process(modeltest.Reply{Calls: []model.ToolCall{{ID: "c1", Name: "delete_note", Args: args}}})
	res, err := first.Start(ctx, thread, []model.Message{model.User("delete note 7")}, event.Discard)
	if err != nil || res.Outcome != agent.Paused {
		t.Fatalf("start: %+v, %v", res, err)
	}

	second := process(modeltest.Reply{Text: "Deleted."})
	pending, err := second.Pending(ctx, thread)
	if err != nil || len(pending) != 1 || pending[0].CallID != "c1" || pending[0].Args != args {
		t.Fatalf("pending in the second process = %+v, %v", pending, err)
	}
	res, err = second.Resume(ctx, thread, []agent.Decision{{CallID: "c1", Verdict: agent.Approve}}, event.Discard)
	if err != nil || res.Answer != "Deleted." || runs != 1 {
		t.Fatalf("resume: %+v, %v, tool ran %d times", res, err, runs)
	}
}
