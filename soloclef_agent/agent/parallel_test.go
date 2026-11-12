package agent_test

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// gauge records how many calls are inside a tool at once, and the most it
// ever saw.
type gauge struct {
	mu          sync.Mutex
	inside, max int
}

func (g *gauge) enter() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.inside++; g.inside > g.max {
		g.max = g.inside
	}
}

func (g *gauge) leave() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inside--
}

func (g *gauge) peak() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.max
}

func TestParallelToolsRunTogetherUpToTheLimitAndAnswerInCallOrder(t *testing.T) {
	const limit = 3
	var g gauge
	// The tool holds each call until `limit` calls are inside it together.
	// If the loop ran them one at a time, that moment would never come and
	// the gate would time out; if it ran more than the limit, peak says so.
	var gate sync.WaitGroup
	gate.Add(limit)
	full := make(chan struct{})
	go func() {
		gate.Wait()
		// Hold the full house for a moment before letting anyone out. A loop
		// that ignored the limit would use it to let a fourth call in, and
		// peak would show it.
		time.Sleep(20 * time.Millisecond)
		close(full)
	}()
	var entered atomic.Int32

	search := tool.Tool{Name: "search", Parameters: []byte(anyObject), Approval: tool.Auto, Parallel: true,
		Run: func(_ context.Context, c tool.Call) (string, error) {
			g.enter()
			defer g.leave()
			if entered.Add(1) <= limit {
				gate.Done()
			}
			select {
			case <-full:
			case <-time.After(5 * time.Second):
				return "", context.DeadlineExceeded
			}
			return "result for " + string(c.Args), nil
		}}

	var calls []model.ToolCall
	for _, id := range []string{"a", "b", "c", "d", "e", "f"} {
		calls = append(calls, call(id, "search", `{"q":"`+id+`"}`))
	}
	m := script(modeltest.Reply{Calls: calls}, modeltest.Reply{Text: "done"})
	a := build(t, agent.Config{Model: m, MaxParallelTools: limit, Tools: []tool.Tool{search}})
	if _, err := a.Start(context.Background(), "t1", user("go"), event.Discard); err != nil {
		t.Fatal(err)
	}

	if g.peak() != limit {
		t.Fatalf("at most %d calls ran together, want exactly the limit %d", g.peak(), limit)
	}
	// Whatever order they finished in, the model is answered in the order it
	// asked.
	var got []string
	for _, line := range roles(m.Requests()[1].Messages)[2:] {
		got = append(got, line[:strings.Index(line, "]")+1])
	}
	equal(t, "result order", got, []string{"tool[a]", "tool[b]", "tool[c]", "tool[d]", "tool[e]", "tool[f]"})
}

func TestToolsNotMarkedParallelRunAloneInTheModelsOrder(t *testing.T) {
	var g gauge
	var mu sync.Mutex
	var order []string
	mk := func(name string, parallel bool) tool.Tool {
		return tool.Tool{Name: name, Parameters: []byte(anyObject), Approval: tool.Auto, Parallel: parallel,
			Run: func(_ context.Context, c tool.Call) (string, error) {
				if !parallel {
					g.enter()
					defer g.leave()
					// Long enough that a second writer, if one were allowed
					// in, would be seen.
					time.Sleep(2 * time.Millisecond)
				}
				mu.Lock()
				order = append(order, c.ID)
				mu.Unlock()
				return "", nil
			}}
	}
	m := script(
		modeltest.Reply{Calls: []model.ToolCall{
			call("w1", "write", `{}`), call("w2", "write", `{}`),
			call("r1", "read", `{}`), call("r2", "read", `{}`),
			call("w3", "write", `{}`),
		}},
		modeltest.Reply{Text: "done"},
	)
	a := build(t, agent.Config{Model: m, MaxParallelTools: 8, Tools: []tool.Tool{mk("write", false), mk("read", true)}})
	if _, err := a.Start(context.Background(), "t1", user("go"), event.Discard); err != nil {
		t.Fatal(err)
	}
	if g.peak() != 1 {
		t.Fatalf("%d writes ran at once, want 1", g.peak())
	}
	// The two reads may finish in either order; everything else is fixed:
	// both writes before them, the last write after.
	got := strings.Join(order, " ")
	if got != "w1 w2 r1 r2 w3" && got != "w1 w2 r2 r1 w3" {
		t.Fatalf("run order = %s", got)
	}
}

func TestAToolTimeoutIsOneFailedResultNotAFailedTurn(t *testing.T) {
	slow, err := tool.WithTimeout(tool.Tool{Name: "slow", Parameters: []byte(anyObject), Approval: tool.Auto,
		Run: func(ctx context.Context, _ tool.Call) (string, error) {
			<-ctx.Done()
			return "", ctx.Err()
		}}, 10*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	m := script(modeltest.Reply{Calls: []model.ToolCall{call("c1", "slow", `{}`)}}, modeltest.Reply{Text: "it timed out"})
	a := build(t, agent.Config{Model: m, Tools: []tool.Tool{slow}})
	res, err := a.Start(context.Background(), "t1", user("go"), event.Discard)
	if err != nil || res.Answer != "it timed out" {
		t.Fatalf("turn: %+v, %v", res, err)
	}
	equal(t, "result", roles(m.Requests()[1].Messages)[2], "tool[c1]:error: slow did not finish within 10ms")
}
