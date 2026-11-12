package threads

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

// The benchmarks run the routes end to end, over HTTP and a real Postgres
// (the same throwaway database as the tests; skipped without it), because
// what these routes cost is their statements and the encoding of what
// those return.
//
//	SOLOCLEF_API_TEST_THREADS_DATABASE=… go test ./internal/chat/threads/ \
//	  -run '^$' -bench . -benchtime 200x

// benchWorld is a world with one long thread (id 20: 200 messages of about
// 2 KB each, half of them with a usage row), 500 more threads in the list,
// and 100,000 saved conversations beside the ones a delete removes.
func benchWorld(b *testing.B) *world {
	b.Helper()
	w := newWorldFor(b)
	body := strings.Repeat("Mitosis has four phases [citation:481]. ", 50)
	w.exec(`INSERT INTO new_chat_threads (id, title, user_id) VALUES (20, 'Long', $1)`, ada)
	w.exec(`INSERT INTO new_chat_messages (id, role, content, thread_id, author_id, turn_id, created_at)
	        SELECT 2000 + n, (CASE WHEN n % 2 = 0 THEN 'USER' ELSE 'ASSISTANT' END)::newchatmessagerole,
	               jsonb_build_array(jsonb_build_object('type', 'text', 'text', $2::text)), 20,
	               CASE WHEN n % 2 = 0 THEN $1::uuid END, '20:' || (n / 2), now() + (n / 2) * interval '1 second'
	        FROM generate_series(0, 199) n`, ada, body)
	w.exec(`INSERT INTO token_usage (prompt_tokens, completion_tokens, total_tokens, cost_micros, model_breakdown, usage_type, thread_id, message_id, user_id)
	        SELECT 100, 20, 120, 300, '{"m": {"total_tokens": 120}}', 'chat', 20, 2000 + n, $1 FROM generate_series(1, 199, 2) n`, ada)
	w.exec(`INSERT INTO new_chat_threads (id, title, archived, updated_at, user_id)
	        SELECT 3000 + n, 'Thread ' || n, n % 10 = 0, now() - n * interval '1 minute', $1
	        FROM generate_series(1, 500) n`, ada)
	w.exec(`INSERT INTO soloclef_agent_checkpoints (thread_id, seq, status, node, state)
	        SELECT 'chat-' || (100000 + n), 1, 'done', '', '{}' FROM generate_series(1, 100000) n`)
	w.exec(`ANALYZE`)
	return w
}

func (w *world) get(b *testing.B, path string) int {
	b.Helper()
	request, _ := http.NewRequest("GET", w.server.URL+path, nil)
	request.Header.Set("X-Test-Caller", ada)
	response, err := w.server.Client().Do(request)
	if err != nil {
		b.Fatal(err)
	}
	defer response.Body.Close()
	raw, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 {
		b.Fatalf("%s: %d %s", path, response.StatusCode, raw)
	}
	return len(raw)
}

// BenchmarkHistory is opening a long thread: the request both apps make
// every time a conversation is shown.
func BenchmarkHistory(b *testing.B) {
	w := benchWorld(b)
	b.ResetTimer()
	size := 0
	for b.Loop() {
		size = w.get(b, "/api/v1/threads/20")
	}
	b.ReportMetric(float64(size), "bytes/answer")
}

// BenchmarkList is the list of an account with 500 threads.
func BenchmarkList(b *testing.B) {
	w := benchWorld(b)
	b.ResetTimer()
	for b.Loop() {
		w.get(b, "/api/v1/threads")
	}
}

// BenchmarkListLimited is the same list asked for its newest 20, which the
// database now cuts instead of the route.
func BenchmarkListLimited(b *testing.B) {
	w := benchWorld(b)
	b.ResetTimer()
	for b.Loop() {
		w.get(b, "/api/v1/threads?limit=20")
	}
}

// BenchmarkShare is making a public link to the long thread: reading it,
// cleaning 200 messages, hashing, and the upsert (the same content each
// time, so every iteration after the first finds the link).
func BenchmarkShare(b *testing.B) {
	w := benchWorld(b)
	b.ResetTimer()
	for b.Loop() {
		if got := w.call(ada, "POST", "/api/v1/threads/20/snapshots", `{}`); got.status != 200 {
			b.Fatalf("share: %d %s", got.status, got.raw)
		}
	}
}

// BenchmarkDelete is deleting a thread with a saved conversation and a
// nested one, among 100,000 other saved conversations: the claim, the tree
// delete and the cascade.
func BenchmarkDelete(b *testing.B) {
	w := benchWorld(b)
	b.ResetTimer()
	i := 0
	for b.Loop() {
		i++
		id := 3000 + i
		if id > 3500 {
			b.Fatal("the benchmark deletes one seeded thread per iteration and has 500; lower -benchtime")
		}
		b.StopTimer()
		w.saveCheckpoints(fmt.Sprintf("chat-%d", id), fmt.Sprintf("chat-%d/call_x", id))
		b.StartTimer()
		if got := w.call(ada, "DELETE", fmt.Sprintf("/api/v1/threads/%d", id), ""); got.status != 200 {
			b.Fatalf("delete: %d %s", got.status, got.raw)
		}
	}
}

// BenchmarkForgetTree is the statement alone, to show what the index is
// for: one tree among 100,000 saved conversations.
func BenchmarkForgetTree(b *testing.B) {
	w := benchWorld(b)
	ctx := context.Background()
	b.ResetTimer()
	i := 0
	for b.Loop() {
		i++
		b.StopTimer()
		w.saveCheckpoints(fmt.Sprintf("chat-%d", i+500000), fmt.Sprintf("chat-%d/call_x", i+500000))
		b.StartTimer()
		if err := w.service.Forget(ctx, int64(i+500000)); err != nil {
			b.Fatal(err)
		}
	}
}
