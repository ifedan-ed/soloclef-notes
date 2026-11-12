package agent_test

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// The benchmarks measure what the loop itself costs, with the two things it
// waits on in production taken away: the model answers at once and the store
// is in memory. What is left is the overhead a turn pays per step and per
// tool call on top of the model's seconds and the tool's own time.

// instantTool does no work, so a benchmark sees only the cost of calling it.
func instantTool(name string, parallel bool) tool.Tool {
	return tool.Tool{Name: name, Parameters: []byte(anyObject), Approval: tool.Auto, Parallel: parallel,
		Run: func(context.Context, tool.Call) (string, error) { return "ok", nil }}
}

// callsThenAnswers is a model that asks for `width` tool calls on each of its
// first `rounds` calls of a turn, then answers. It decides from the request,
// so it serves any number of turns.
func callsThenAnswers(rounds, width int) model.Model {
	batch := make([]model.ToolCall, width)
	for i := range batch {
		batch[i] = model.ToolCall{Name: "work", Args: `{"n":1}`}
	}
	return modeltest.Func(func(_ context.Context, req model.Request, _ func(model.Chunk)) (model.Response, error) {
		asked := 0
		for i := len(req.Messages) - 1; i >= 0 && req.Messages[i].Role != model.RoleUser; i-- {
			if req.Messages[i].Role == model.RoleAssistant {
				asked++
			}
		}
		if asked < rounds {
			return model.Response{Message: model.Message{ToolCalls: batch}}, nil
		}
		return model.Response{Message: model.Message{Content: "done"}}, nil
	})
}

func benchAgent(b *testing.B, m model.Model, parallel bool) *agent.Agent {
	b.Helper()
	a, err := agent.New(agent.Config{
		Model: m, Tools: []tool.Tool{instantTool("work", parallel)},
		MaxSteps: 1000, MaxParallelTools: 8, Store: checkpoint.NewMemory(),
	})
	if err != nil {
		b.Fatal(err)
	}
	return a
}

// turns runs b.N turns, each on a thread of its own so that every turn starts
// from the same empty conversation, and reports the cost per unit of work.
func turns(b *testing.B, a *agent.Agent, perTurn int, unit string) {
	b.Helper()
	ctx := context.Background()
	input := []model.Message{model.User("go")}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.Start(ctx, strconv.Itoa(i), input, event.Discard); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*perTurn), "ns/"+unit)
}

// BenchmarkStep is the overhead of one step: a turn of ten model calls each
// followed by one tool call, then the answer. 21 steps, each one saved.
func BenchmarkStep(b *testing.B) {
	turns(b, benchAgent(b, callsThenAnswers(10, 1), false), 21, "step")
}

// BenchmarkToolCall is the overhead of one tool call, by how the batch runs.
// The model asks for eight calls at once; the turn is model, tools, model.
func BenchmarkToolCall(b *testing.B) {
	b.Run("alone", func(b *testing.B) {
		turns(b, benchAgent(b, callsThenAnswers(1, 1), false), 1, "call")
	})
	b.Run("eight-in-order", func(b *testing.B) {
		turns(b, benchAgent(b, callsThenAnswers(1, 8), false), 8, "call")
	})
	b.Run("eight-in-parallel", func(b *testing.B) {
		turns(b, benchAgent(b, callsThenAnswers(1, 8), true), 8, "call")
	})
}

// BenchmarkStepByHistory shows what the per-step save costs as a thread
// grows: every step encodes the whole conversation. Each message here is
// about 1 KB, so "200" is a 200 KB thread. A turn is one model call that
// answers at once: one step, two saves (the claim and the step).
//
// The thread is put back to its seeded size before every turn, outside the
// timer. Without that, each turn would leave two more messages behind and a
// long benchmark would measure a thread thousands of messages long.
func BenchmarkStepByHistory(b *testing.B) {
	for _, size := range []int{0, 50, 200, 1000} {
		b.Run(fmt.Sprintf("%d-messages", size), func(b *testing.B) {
			ctx := context.Background()
			a := benchAgent(b, callsThenAnswers(0, 0), false)
			// Ordinary prose, because that is what a thread holds and JSON's
			// cost depends on it: text full of characters that need escaping
			// would measure the escaping.
			body := strings.Repeat("plain words of an answer ", 41)[:1024]
			history := make([]model.Message, size)
			for i := range history {
				history[i] = model.Message{Role: model.RoleUser, Content: body}
			}
			reset := func(st *agent.State) error {
				st.Messages = append(st.Messages[:0], history...)
				return nil
			}
			if _, err := a.Start(ctx, "t", []model.Message{model.User("seed")}, event.Discard); err != nil {
				b.Fatal(err)
			}
			input := []model.Message{model.User("go")}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				if err := a.Edit(ctx, "t", reset); err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
				if _, err := a.Start(ctx, "t", input, event.Discard); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkToolHooks is what hooks add to a tool call: none, and a stack of
// four that each call the next and return its result.
func BenchmarkToolHooks(b *testing.B) {
	pass := func(ctx context.Context, call tool.Call, next tool.Func) (string, error) { return next(ctx, call) }
	for _, n := range []int{0, 4} {
		b.Run(fmt.Sprintf("%d-hooks", n), func(b *testing.B) {
			hooks := make([]agent.ToolHook, n)
			for i := range hooks {
				hooks[i] = pass
			}
			a, err := agent.New(agent.Config{
				Model: callsThenAnswers(1, 1), Tools: []tool.Tool{instantTool("work", false)},
				MaxSteps: 10, MaxParallelTools: 1, Store: checkpoint.NewMemory(), ToolHooks: hooks,
			})
			if err != nil {
				b.Fatal(err)
			}
			turns(b, a, 1, "call")
		})
	}
}

// BenchmarkNew is the cost of building an agent with forty tools and two
// delegates. It is run per turn in a chat, from that turn's own tool list
// and permissions, so it has to be cheap enough that nobody is tempted to
// cache agents between turns.
func BenchmarkNew(b *testing.B) {
	store := checkpoint.NewMemory()
	m := callsThenAnswers(0, 0)
	tools := make([]tool.Tool, 40)
	for i := range tools {
		tools[i] = instantTool("tool_"+strconv.Itoa(i), i%2 == 0)
		tools[i].Description = "Does one thing among forty."
		tools[i].Parameters = []byte(`{"type":"object","properties":{"query":{"type":"string"},"limit":{"type":"integer"}},"required":["query"]}`)
	}
	specialist, err := agent.New(agent.Config{Model: m, Tools: tools[:10], MaxSteps: 20, MaxParallelTools: 4, Store: store})
	if err != nil {
		b.Fatal(err)
	}
	cfg := agent.Config{
		Model: m, System: "You are helpful.", Tools: tools, MaxSteps: 80, MaxParallelTools: 8, Store: store,
		Delegates: []agent.Delegate{
			{Name: "knowledge_base", Description: "Edits notes.", Agent: specialist},
			{Name: "deliverables", Description: "Makes artifacts.", Agent: specialist},
		},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := agent.New(cfg); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkTextDelta is the cost of carrying one streamed token from the
// model adapter to the sink: the hot path of a chat.
func BenchmarkTextDelta(b *testing.B) {
	const tokens = 1000
	m := modeltest.Func(func(_ context.Context, _ model.Request, stream func(model.Chunk)) (model.Response, error) {
		for i := 0; i < tokens; i++ {
			stream(model.Chunk{Kind: model.ChunkText, Text: "token "})
		}
		return model.Response{Message: model.Message{Content: "done"}}, nil
	})
	a := benchAgent(b, m, false)
	seen := 0
	sink := func(ev event.Event) {
		if ev.Kind == event.TextDelta {
			seen++
		}
	}
	ctx := context.Background()
	input := []model.Message{model.User("go")}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := a.Start(ctx, strconv.Itoa(i), input, sink); err != nil {
			b.Fatal(err)
		}
	}
	if seen != b.N*tokens {
		b.Fatalf("sink saw %d deltas, want %d", seen, b.N*tokens)
	}
	b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N*tokens), "ns/token")
}

// BenchmarkDelegate is the overhead of handing a task to a nested agent and
// getting its answer back: a thread of its own opened, run for two model
// calls and one tool call with every step saved, and removed. It is the
// whole cost of the boundary between two runs, since nothing is merged when
// the nested one returns.
func BenchmarkDelegate(b *testing.B) {
	store := checkpoint.NewMemory()
	specialist, err := agent.New(agent.Config{
		Model: callsThenAnswers(1, 1), Tools: []tool.Tool{instantTool("work", false)},
		MaxSteps: 10, MaxParallelTools: 1, Store: store,
	})
	if err != nil {
		b.Fatal(err)
	}
	hand := []model.ToolCall{{Name: "specialist", Args: `{"task":"do the work"}`}}
	main, err := agent.New(agent.Config{
		Model: modeltest.Func(func(_ context.Context, req model.Request, _ func(model.Chunk)) (model.Response, error) {
			if req.Messages[len(req.Messages)-1].Role == model.RoleTool {
				return model.Response{Message: model.Message{Content: "done"}}, nil
			}
			return model.Response{Message: model.Message{ToolCalls: hand}}, nil
		}),
		Delegates: []agent.Delegate{{Name: "specialist", Description: "Does the work.", Agent: specialist}},
		MaxSteps:  10, MaxParallelTools: 1, Store: store,
	})
	if err != nil {
		b.Fatal(err)
	}
	turns(b, main, 1, "delegation")
}
