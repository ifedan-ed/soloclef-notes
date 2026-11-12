package chat

import (
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// instant is a model that answers at once: a search, then a 400-word answer
// streamed word by word. With it, a turn's time is this tier's own: the
// routes, the rows, the loop, the frames and the saved parts.
func instant() model.Model {
	answer := strings.Repeat("word ", 399) + "[1]."
	return modeltest.Func(func(ctx context.Context, req model.Request, stream func(model.Chunk)) (model.Response, error) {
		last := req.Messages[len(req.Messages)-1]
		if last.Role == model.RoleTool {
			for _, word := range strings.SplitAfter(answer, " ") {
				stream(model.Chunk{Kind: model.ChunkText, Text: word})
			}
			return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: answer}, FinishReason: "stop"}, nil
		}
		call := model.ToolCall{ID: "c1", Name: "search_knowledge_base", Args: `{"query":"mitosis"}`}
		stream(model.Chunk{Kind: model.ChunkToolStart, ID: call.ID, Name: call.Name})
		stream(model.Chunk{Kind: model.ChunkToolArgs, Text: call.Args})
		return model.Response{Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{call}}, FinishReason: "tool_calls"}, nil
	})
}

// BenchmarkTurn is one whole turn over HTTP: request in, two model calls and
// a tool call, about 420 frames out, the rows written. Each turn is on a new
// thread, so the transcript does not grow from one iteration to the next.
func BenchmarkTurn(b *testing.B) {
	a := newApp(b, instant(), []tool.Tool{library()})
	b.ReportAllocs()
	next := int64(1000)
	for b.Loop() {
		next++
		a.rows.mu.Lock()
		a.rows.threads[next] = &Thread{ID: next, UserID: ada}
		a.rows.mu.Unlock()
		response := a.request(ada, http.MethodPost, TurnPath, `{"chat_id":`+itoa(next)+`,"user_query":"What are the phases?"}`)
		n, _ := io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != 200 || n < 20_000 {
			b.Fatalf("status %d, %d bytes", response.StatusCode, n)
		}
	}
}

// BenchmarkBuild is building a turn's agent with the production assembly:
// the account's prompt, the connected providers, every tool, the
// specialist. It is done on every turn and on every "what is waiting".
func BenchmarkBuild(b *testing.B) {
	script := modeltest.NewScript()
	assembly, _, _, _ := production(b, script, script, newRows(), checkpoint.NewMemory(), connected{"exa": true, "tavily": true})
	turn := Turn{ThreadID: 1, UserID: ada, Scope: "all"}
	b.ReportAllocs()
	for b.Loop() {
		if _, err := assembly.Build(context.Background(), turn); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStepDisplay is what the step display adds to a tool call: the
// snapshot when it starts and the one when it ends.
func BenchmarkStepDisplay(b *testing.B) {
	now := time.Date(2026, 11, 11, 9, 0, 0, 0, time.UTC)
	steps := newJournal(func() time.Time { return now }, nil)
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		id := strconv.Itoa(i)
		steps.observe(event.Event{Kind: event.ToolCallReady, CallID: id, Tool: "search_knowledge_base"})
		steps.observe(event.Event{Kind: event.ToolResult, CallID: id, Tool: "search_knowledge_base"})
	}
}

// BenchmarkModelFor is choosing a turn's model with the proxy's list
// already read: the cost every turn pays for the model being per account.
func BenchmarkModelFor(b *testing.B) {
	models, _, chosen := menu(instant())
	_ = chosen.ChooseChatModel(context.Background(), ada, "other-model")
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := models.For(context.Background(), ada); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCutPoint finds where a 400-message conversation is cut for a
// summary.
func BenchmarkCutPoint(b *testing.B) {
	var conversation []model.Message
	for i := 0; i < 100; i++ {
		conversation = append(conversation, model.User("a question"), model.Message{Role: model.RoleAssistant, ToolCalls: call("c", "t", "{}")},
			model.Message{Role: model.RoleTool, ToolCallID: "c", Content: strings.Repeat("passage ", 300)}, model.Message{Role: model.RoleAssistant, Content: "an answer"})
	}
	b.ReportAllocs()
	for b.Loop() {
		if cutPoint(conversation, 60_000) == 0 {
			b.Fatal("no cut")
		}
	}
}
