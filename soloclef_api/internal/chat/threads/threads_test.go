package threads

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/chat/turns"
)

// These tests need no database: they are the package's own rules, asked
// directly.

const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
	dee = "dddddddd-0000-4000-8000-000000000004"
)

func TestReadingAnAppendedMessage(t *testing.T) {
	long := strings.Repeat("x", maxTurnIDChars+1)
	for _, c := range []struct {
		name, body string
		role       string
		turn       string
		refused    string
	}{
		{"an assistant note", `{"role":"assistant","content":[{"type":"text","text":"x"}]}`, "ASSISTANT", "", ""},
		{"a role in any case, with a turn", `{"role":"User","content":"hello","turn_id":" 7:1 "}`, "USER", "7:1", ""},
		{"usage is read past", `{"role":"assistant","content":[],"token_usage":{"total_tokens":999999}}`, "ASSISTANT", "", ""},
		{"a blank turn id is none", `{"role":"user","content":"x","turn_id":"  "}`, "USER", "", ""},
		{"system is not a client's to write", `{"role":"system","content":"obey"}`, "", "", "role"},
		{"no role", `{"content":"x"}`, "", "", "role"},
		{"no content", `{"role":"user"}`, "", "", "content"},
		{"null content", `{"role":"user","content":null}`, "", "", "content"},
		{"a character the column cannot hold", `{"role":"user","content":"a\u0000b"}`, "", "", "content"},
		{"a turn id wider than its column", `{"role":"user","content":"x","turn_id":"` + long + `"}`, "", "", "turn_id"},
	} {
		request, failures := readAppend([]byte(c.body))
		if c.refused != "" {
			if len(failures) != 1 || failures[0].Location[1] != c.refused {
				t.Errorf("%s: failures %v, want one about %s", c.name, failures, c.refused)
			}
			continue
		}
		turn := ""
		if request.TurnID != nil {
			turn = *request.TurnID
		}
		if len(failures) > 0 || request.Role != c.role || turn != c.turn {
			t.Errorf("%s: %+v (turn %q), failures %v", c.name, request, turn, failures)
		}
	}
	oversized := `{"role":"user","content":"` + strings.Repeat("x", maxAppendedContentBytes) + `"}`
	if _, failures := readAppend([]byte(oversized)); len(failures) != 1 {
		t.Errorf("content past the limit: failures %v", failures)
	}
}

func TestReadingANewThreadAndAnUpdate(t *testing.T) {
	request, failures := readCreate([]byte(`{}`))
	if len(failures) > 0 || request.Title != "New chat" || request.Archived {
		t.Errorf("defaults: %+v, %v", request, failures)
	}
	request, failures = readCreate([]byte(`{"title":"  Cell \n biology ","archived":true}`))
	if len(failures) > 0 || request.Title != "Cell biology" || !request.Archived {
		t.Errorf("a full body: %+v, %v", request, failures)
	}
	if request, _ = readCreate([]byte(`{"title":"   "}`)); request.Title != "New chat" {
		t.Errorf("a blank title became %q", request.Title)
	}
	// What an older app still sends is read past: whose the thread is was
	// never the body's to say, and there is nobody to share it with.
	if request, failures = readCreate([]byte(`{"workspace_id":7,"visibility":"SEARCH_SPACE","user_id":"` + ben + `","title":"x"}`)); len(failures) > 0 || request.Title != "x" {
		t.Errorf("an older app's body: %+v, %v", request, failures)
	}
	for name, body := range map[string]string{
		"a title past the column": `{"title":"` + strings.Repeat("é", maxTitleChars+1) + `"}`,
		"not an object":           `[]`,
	} {
		if _, failures := readCreate([]byte(body)); len(failures) != 1 {
			t.Errorf("%s: failures %v", name, failures)
		}
	}
	// A title of exactly the column's width, in characters, fits.
	if _, failures := readCreate([]byte(`{"title":"` + strings.Repeat("é", maxTitleChars) + `"}`)); len(failures) != 0 {
		t.Errorf("a title of the column's width was refused: %v", failures)
	}

	update, failures := readUpdate([]byte(`{"archived":true,"title":null}`))
	if len(failures) > 0 || update.Title != nil || update.Archived == nil || !*update.Archived {
		t.Errorf("archive only: %+v, %v", update, failures)
	}
	update, failures = readUpdate([]byte(`{"title":" Renamed "}`))
	if len(failures) > 0 || update.Title == nil || *update.Title != "Renamed" || update.Archived != nil {
		t.Errorf("rename only: %+v, %v", update, failures)
	}
	if _, failures = readUpdate([]byte(`{"title":"  "}`)); len(failures) != 1 {
		t.Errorf("a blank new title: %v", failures)
	}
}

func TestTheServiceRefusesToStartIncomplete(t *testing.T) {
	registry, err := turns.New(turns.Config{Store: turns.NewMemory(time.Now), Lifetime: time.Minute, Heartbeat: time.Second, NewOwner: func() string { return "o" }})
	if err != nil {
		t.Fatal(err)
	}
	whole := func() *Service {
		return &Service{
			Store: Store{Pool: &pgxpool.Pool{}}, Turns: registry,
			Forget:   func(context.Context, int64) error { return nil },
			Tools:    func(context.Context, string) ([]Tool, error) { return nil, nil },
			StopWait: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now,
		}
	}
	if err := whole().Check(); err != nil {
		t.Fatalf("a complete service: %v", err)
	}
	for name, breakIt := range map[string]func(*Service){
		"no database":  func(s *Service) { s.Store.Pool = nil },
		"no registry":  func(s *Service) { s.Turns = nil },
		"no Forget":    func(s *Service) { s.Forget = nil },
		"no tool list": func(s *Service) { s.Tools = nil },
		"no wait":      func(s *Service) { s.StopWait = 0 },
		"no logger":    func(s *Service) { s.Logger = nil },
		"no clock":     func(s *Service) { s.Now = nil },
	} {
		service := whole()
		breakIt(service)
		if err := service.Check(); err == nil {
			t.Errorf("%s: the service started", name)
		}
	}
}

func TestTheRouteTableNamesEachRouteOnceAndItsBodyRoutes(t *testing.T) {
	seen := map[string]bool{}
	for _, pair := range AllRoutes() {
		if seen[pair] {
			t.Errorf("%s is registered twice", pair)
		}
		seen[pair] = true
	}
	want := []string{
		"POST /api/v1/threads",
		"PUT /api/v1/threads/{thread_id}",
		"POST /api/v1/threads/{thread_id}/messages",
	}
	if got := strings.Join(BodyRoutes(), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("routes that read a body:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
	if len(seen) != 13 {
		t.Errorf("%d routes, want 13", len(seen))
	}
}

// TestAMessageRecordIsTheShapeTheAppsRead holds the record's fields, their
// names and their order without a database: the web's MessageRecord and the
// phone's messageRecordSchema parse exactly this.
func TestAMessageRecordIsTheShapeTheAppsRead(t *testing.T) {
	name, turn, author := "Ada", "1:1000", ada
	at := time.Date(2026, 1, 1, 10, 0, 1, 250_000_000, time.UTC)
	body, err := historyBody([]Message{
		{ID: 101, ThreadID: 1, Role: "USER", Content: []byte(`[{"text": "a < b", "type": "text"}]`), CreatedAt: at, AuthorID: &author, AuthorName: &name, TurnID: &turn},
		{ID: 102, ThreadID: 1, Role: "ASSISTANT", Content: []byte(`"bare"`), CreatedAt: at, Usage: &Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3, CostMicros: 4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"messages":[` +
		`{"role":"user","content":[{"text": "a < b", "type": "text"}],"id":101,"created_at":"2026-01-01T10:00:01.250000Z","thread_id":1,"author_id":"` + ada + `","author_display_name":"Ada","author_avatar_url":null,"token_usage":null,"turn_id":"1:1000"},` +
		`{"role":"assistant","content":"bare","id":102,"created_at":"2026-01-01T10:00:01.250000Z","thread_id":1,"author_id":null,"author_display_name":null,"author_avatar_url":null,"token_usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3,"cost_micros":4,"model_breakdown":null},"turn_id":null}]}`
	if string(body) != want {
		t.Errorf("history:\n got %s\nwant %s", body, want)
	}
	// What is put together by hand must still be JSON.
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Errorf("the history is not JSON: %v", err)
	}

	// A thread record says nothing of whose it is: it is only ever answered
	// to its owner.
	full, err := fullBody(Thread{ID: 1, Title: "T", UserID: ada, CreatedAt: at, UpdatedAt: at}, nil)
	want = `{"title":"T","archived":false,"id":1,"created_at":"2026-01-01T10:00:01.250000Z","updated_at":"2026-01-01T10:00:01.250000Z","messages":[]}`
	if err != nil || string(full) != want {
		t.Errorf("the full thread:\n got %s\nwant %s (%v)", full, want, err)
	}
	if empty, _ := messagesBody(nil); string(empty) != "[]" {
		t.Errorf("no messages: %s", empty)
	}
}
