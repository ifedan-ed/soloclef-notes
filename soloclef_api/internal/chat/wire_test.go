package chat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentdeletion"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/indexqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/knowledgebase"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/voices"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

func complete() Settings {
	return Settings{
		ModelURL: "http://proxy.example/v1", ModelKey: "k", ChatModel: "chat", EmbeddingModel: "embed", EmbeddingDimension: 3,
		QdrantURL: "http://qdrant.example:6333", QdrantCollection: "chunks", QdrantHNSWEf: 200, QdrantQuantized: true, QdrantOversampling: 2,
		NCBITool: "soloclef", ValkeyURL: "redis://valkey.example:6379/0", Brand: "Soloclef Notes",
		MaxSteps: 40, MaxTurn: 5 * time.Minute, SecretKey: "fake-secret-key",
	}
}

// A chat that is half configured does not start, and says everything that
// is missing in one message.
func TestTheChatRefusesToStartWithSettingsMissing(t *testing.T) {
	if err := complete().check(); err != nil {
		t.Fatalf("complete settings were refused: %v", err)
	}
	err := Settings{QdrantQuantized: true}.check()
	if err == nil {
		t.Fatal("empty settings were accepted")
	}
	for _, name := range []string{"ModelURL", "ModelKey", "ChatModel", "EmbeddingModel", "EmbeddingDimension", "QdrantURL", "QdrantCollection", "QdrantHNSWEf", "QdrantOversampling", "NCBITool", "ValkeyURL", "Brand", "MaxSteps", "MaxTurn"} {
		if !strings.Contains(err.Error(), name) {
			t.Errorf("the refusal does not name %s: %v", name, err)
		}
	}
	// Optional ones are optional.
	optional := complete()
	optional.QdrantAPIKey, optional.NCBIEmail, optional.NCBIAPIKey, optional.QdrantQuantized, optional.QdrantOversampling = "", "", "", false, 0
	if err := optional.check(); err != nil {
		t.Errorf("optional settings were required: %v", err)
	}
	// Settings are checked before anything is opened, and so are the parts.
	if _, _, err := NewService(context.Background(), Settings{}, Deps{}); err == nil {
		t.Error("a service was built from nothing")
	}
	if _, _, err := NewService(context.Background(), complete(), Deps{}); err == nil || !strings.Contains(err.Error(), "database pool") {
		t.Errorf("a service was built with no database: %v", err)
	}
}

// proxy stands in for the model proxy: the chat completions stream and the
// embeddings route, in the OpenAI wire format the proxy speaks.
func proxy(t *testing.T) (*httptest.Server, *toolsSeen) {
	seen := &toolsSeen{}
	send := func(w http.ResponseWriter, chunks ...string) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			_, _ = io.WriteString(w, "data: "+chunk+"\n\n")
			w.(http.Flusher).Flush()
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}
	usage := `{"choices":[],"usage":{"prompt_tokens":40,"completion_tokens":8,"total_tokens":48}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer proxy-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/v1/models":
			// The proxy's own list: the chat model, and the embedding
			// model, which the chat must not offer as one.
			_, _ = io.WriteString(w, `{"data":[{"id":"chat-model"},{"id":"`+complete().EmbeddingModel+`"}],"object":"list"}`)
		case "/v1/model/info":
			_, _ = io.WriteString(w, `{"data":[{"model_name":"chat-model","model_info":{"mode":"chat"}}]}`)
		case "/v1/embeddings":
			_, _ = io.WriteString(w, `{"data":[{"embedding":[0.1,0.2,0.3]}]}`)
		case "/v1/chat/completions":
			var request struct {
				Tools []struct {
					Function struct {
						Name string `json:"name"`
					} `json:"function"`
				} `json:"tools"`
				Model    string `json:"model"`
				Messages []struct {
					Role    string `json:"role"`
					Content string `json:"content"`
				} `json:"messages"`
			}
			_ = json.Unmarshal(raw, &request)
			if len(request.Tools) > 0 {
				var names []string
				for _, offered := range request.Tools {
					names = append(names, offered.Function.Name)
				}
				seen.set(names)
			}
			last := request.Messages[len(request.Messages)-1]
			switch {
			case request.Model != "chat-model":
				w.WriteHeader(http.StatusNotFound)
			case strings.HasPrefix(last.Content, "Write a title"):
				send(w, `{"choices":[{"index":0,"delta":{"content":"Cell division"}}]}`, `{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, usage)
			case last.Role == "tool":
				send(w, `{"choices":[{"index":0,"delta":{"content":"Mitosis has four "}}]}`, `{"choices":[{"index":0,"delta":{"content":"phases [1]."}}]}`,
					`{"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`, usage)
			default:
				send(w, `{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"k1","type":"function","function":{"name":"search_knowledge_base","arguments":"{\"query\":"}}]}}]}`,
					`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"mitosis phases\"}"}}]}}]}`,
					`{"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`, usage)
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server, seen
}

// toolsSeen is the tools the proxy was last asked to offer the model.
type toolsSeen struct {
	mu    sync.Mutex
	names []string
}

func (s *toolsSeen) set(names []string) {
	s.mu.Lock()
	s.names = names
	s.mu.Unlock()
}

func (s *toolsSeen) get() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(s.names, " ")
}

// noVoices is a deployment with no speech service: a podcast asked for in
// chat is refused in a sentence, and nothing else is affected.
type noVoices struct{}

func (noVoices) Roster(context.Context) (*voices.Roster, error) {
	return nil, errors.New("no speech service in this test")
}

// queued records what was put on the queue.
type queued struct {
	mu       sync.Mutex
	messages []produce.Message
}

func (q *queued) Enqueue(_ context.Context, message produce.Message) (string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.messages = append(q.messages, message)
	return "task-1", nil
}

// TestTheChatAsAServerRunsIt builds the chat exactly as cmd/api will, with
// NewService, and asks it a question over HTTP. Postgres and Valkey are
// real; the model proxy and the vector store are stand-ins that speak their
// wire formats. It is the one test in which the model client, the query
// embedding, the Qdrant request, the keyword SQL, the checkpoint table, the
// Valkey lease and the routes all run together.
//
// It needs the throwaway Postgres of TestTheStoresOverARealDatabase and a
// throwaway Valkey (SOLOCLEF_API_TEST_VALKEY_URL), and is skipped without
// either. It reseeds the database.
func TestTheChatAsAServerRunsIt(t *testing.T) {
	address, valkeyURL := os.Getenv(databaseVariable), os.Getenv("SOLOCLEF_API_TEST_VALKEY_URL")
	if address == "" || valkeyURL == "" {
		t.Skipf("%s and SOLOCLEF_API_TEST_VALKEY_URL are not both set; this test needs a throwaway Postgres and Valkey", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	models, offeredTools := proxy(t)
	var vectorQuery map[string]any
	qdrant := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &vectorQuery)
		// A chunk of this account's, and one of another's that a broken
		// filter might return: the second must not reach the model.
		_, _ = io.WriteString(w, `{"result":{"points":[{"id":900,"score":0.95},{"id":481,"score":0.9}]}}`)
	}))
	defer qdrant.Close()
	local, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lifetime, shutdown := context.WithCancel(ctx)
	defer shutdown()
	settings := complete()
	settings.ModelURL, settings.ModelKey, settings.ChatModel = models.URL+"/v1", "proxy-key", "chat-model"
	settings.QdrantURL, settings.ValkeyURL = qdrant.URL, valkeyURL
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	queue := &queued{}
	started := time.Now()
	service, release, err := NewService(ctx, settings, Deps{Pool: pool, Files: local, Queue: queue, HTTP: &http.Client{}, Voices: noVoices{}, Logger: logger, Lifetime: lifetime,
		Parsers: parserchoicetest.Registry(t, parse.Local),
		KnowledgeBase: knowledgebase.Changes{
			Pool: pool, Logger: logger,
			Indexing: indexqueue.Indexing{Queue: queue, Marker: indexqueue.PostgresMarker{Pool: pool}, Logger: logger},
			Vectors:  documentdeletion.Qdrant{URL: qdrant.URL, Collection: settings.QdrantCollection, HTTP: &http.Client{}},
		}})
	if err != nil {
		t.Fatalf("building the chat: %v", err)
	}
	built := time.Since(started)
	defer release()
	defer service.Wait()
	// Seeded after the service is built: NewService creates the checkpoint
	// table the seed empties.
	if _, err := pool.Exec(ctx, seed); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	// The seed's connected apps are given saved tool lists, so that no
	// turn of this test asks a server on the internet for one: an app with
	// one tool, and one with none.
	if _, err := pool.Exec(ctx, `
UPDATE search_source_connectors SET config = '{"server_config": {"transport": "streamable-http", "url": "https://mcp.browserbase.com/mcp", "headers": {"x-api-key": "fake-key"}}, "cached_tools": {"tools": [{"name": "lookup", "description": "Look a thing up.", "input_schema": {"type": "object"}}]}}' WHERE id = 1;
UPDATE search_source_connectors SET config = config || '{"cached_tools": {"tools": []}}'::jsonb WHERE id IN (2, 3);`); err != nil {
		t.Fatalf("seeding the connected apps: %v", err)
	}

	mux := http.NewServeMux()
	api := humago.New(mux, huma.DefaultConfig("test", "0.0.1"))
	Routes{Service: service, Errors: errorenvelope.Writer{ReportURL: "https://notes.example/contact"}}.Register(api)
	withCaller := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(principal.WithPrincipal(r.Context(), principal.Principal{UserID: ada, Method: principal.MethodSession})))
	})
	bodies := requestbody.Middleware{Covers: func(r *http.Request) bool { return r.ContentLength != 0 }}
	server := httptest.NewServer(requestcontext.Middleware(logger, bodies.Wrap(withCaller)))
	defer server.Close()

	sent := time.Now()
	response, err := server.Client().Post(server.URL+TurnPath, "application/json", strings.NewReader(`{"chat_id":1,"user_query":"What are the phases of mitosis?"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("status %d: %s", response.StatusCode, raw)
	}
	frames, done := readFrames(t, response.Body)
	took := time.Since(sent)
	page := readAll(t, frames)
	if !done || page.failure != nil {
		t.Fatalf("done=%v failure=%v\n%s", done, page.failure, kinds(frames))
	}
	want := "start start-step data-turn-info data-turn-status data-user-message-id data-assistant-message-id data-activity-timing " +
		"tool-input-start tool-input-delta tool-input-delta tool-input-available data-activity tool-output-available data-activity " +
		"text-start text-delta text-delta text-end data-activity-timing data-thread-title-update data-token-usage data-turn-status finish-step finish"
	if got := kinds(frames); got != want {
		t.Fatalf("frames\n got: %s\nwant: %s", got, want)
	}
	// A documents-only turn of a plain chat: the search, the account's
	// connected app (which asks first), the memory and the specialist. Nothing of the
	// web, which this message did not ask for.
	// The memory tool is in every chat: the account's own memory, to write.
	if got := offeredTools.get(); got != "search_knowledge_base update_memory lookup deliverables knowledge_base" {
		t.Errorf("the model was offered: %s", got)
	}

	// The model menu, from the proxy's own list: the embedding model is
	// not a chat model. Choosing is stored and read back.
	menu := func(method, body string) map[string]any {
		t.Helper()
		request, _ := http.NewRequest(method, server.URL+ChatModelPath, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		answered, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer answered.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(answered.Body).Decode(&out)
		if answered.StatusCode != 200 {
			t.Fatalf("%s chat-model: %d %v", method, answered.StatusCode, out)
		}
		return out
	}
	if read := menu(http.MethodGet, ""); read["model"] != "chat-model" || read["selected"] != nil || len(read["models"].([]any)) != 1 {
		t.Errorf("the model menu = %v", read)
	}
	if chosen := menu(http.MethodPut, `{"model":"chat-model"}`); chosen["selected"] != "chat-model" {
		t.Errorf("after choosing = %v", chosen)
	}

	// The vector store was asked about this account only: the tenant
	// condition is first, on the indexer's field, with the asker's uuid.
	tenant := vectorQuery["filter"].(map[string]any)["must"].([]any)[0].(map[string]any)
	if tenant["key"] != "user_id" || tenant["match"].(map[string]any)["value"] != ada || len(vectorQuery["query"].([]any)) != 3 {
		t.Errorf("vector query = %v", vectorQuery)
	}
	// The rows, as the page will reload them.
	var saved, title string
	_ = pool.QueryRow(ctx, `SELECT content::text FROM new_chat_messages WHERE thread_id = 1 AND role = 'ASSISTANT'`).Scan(&saved)
	_ = pool.QueryRow(ctx, `SELECT title FROM new_chat_threads WHERE id = 1`).Scan(&title)
	if !strings.Contains(saved, "Mitosis has four phases [citation:481].") || title != "Cell division" {
		t.Errorf("saved %s, title %q", saved, title)
	}
	// What the model was shown came from this account's chunk alone.
	var state []byte
	_ = pool.QueryRow(ctx, `SELECT state FROM soloclef_agent_checkpoints WHERE thread_id = 'chat-1'`).Scan(&state)
	if !strings.Contains(string(state), "prophase, metaphase") || strings.Contains(string(state), "another account wrote") {
		t.Errorf("the transcript:\n%s", state)
	}
	var tokens int
	_ = pool.QueryRow(ctx, `SELECT total_tokens FROM token_usage WHERE thread_id = 1`).Scan(&tokens)
	if tokens != 96 {
		t.Errorf("metered %d tokens, want 96 (two calls of 48; the title is not the answer's)", tokens)
	}
	// The thread is free, as Valkey tells any process.
	if running, err := service.Turns.Running(ctx, "1"); err != nil || running {
		t.Errorf("running=%v err=%v after the turn", running, err)
	}
	t.Logf("built the chat in %s; a turn with one search, over real Postgres and Valkey, took %s", built.Round(time.Microsecond), took.Round(time.Microsecond))
}
