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

func TestPublicTextLosesEverySourceMarkerAndNothingElse(t *testing.T) {
	for _, c := range []struct{ name, in, want string }{
		{"a passage marker, with the space before it", "Mitosis has four phases [citation:481].", "Mitosis has four phases."},
		{"the older document form", "Stated [citation:doc-12] twice.", "Stated twice."},
		{"an address, which the old pattern left in", "See [citation:https://example.org/a?b=1] here.", "See here."},
		{"full-width brackets, as some models write them", "Fact【citation:7】.", "Fact."},
		{"a zero-width space inside", "Fact [\u200bcitation:7].", "Fact."},
		{"several in a row", "Both [citation:1][citation:https://x.test/]\nNext line", "Both\nNext line"},
		{"a bare label is not a marker", "Array index [1] and [citation] stay.", "Array index [1] and [citation] stay."},
		{"the word alone is not a marker", "The citation: Smith 2020.", "The citation: Smith 2020."},
		{"only markers", " [citation:1] ", ""},
		{"a marker never closed is text", "Open [citation:12 and on\nwith a [link] later", "Open [citation:12 and on\nwith a [link] later"},
		{"the word in prose before a real marker", "A citation: yes [citation:4]!", "A citation: yes!"},
		{"a marker between words", "word [citation:1] next\t[citation:2]", "word next"},
		// The text around a marker is the author's: indentation is code
		// and nested lists, and a blank line is a paragraph.
		{"code keeps its indentation", "```python\ndef f():\n    return  1\n```\nDone [citation:3].", "```python\ndef f():\n    return  1\n```\nDone."},
		{"a nested list keeps its shape", "- a\n  - b [citation:2]\n\n\n- c", "- a\n  - b\n\n\n- c"},
	} {
		if got := publicText(c.in); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestPublicPartsKeepTextAndPublicCardsOnly(t *testing.T) {
	content := `[
	  {"type":"reasoning","text":"private thinking","id":"r1","status":"completed","startedAt":"x"},
	  {"type":"text","text":"Answer [citation:481]"},
	  {"type":"text","text":"[citation:9]"},
	  {"type":"tool-call","toolCallId":"c1","toolName":"search_knowledge_base","args":{"query":"secret"},"result":"passages"},
	  {"type":"tool-call","toolCallId":"c2","toolName":"save_artifact","args":{"title":"Deck"},
	   "result":{"artifact_id":5,"storage_key":"ws/7/deck.json","files":[{"file_path":"/srv/x","name":"deck.json","user_id":"u"}],"token":"t"}},
	  {"type":"status","code":"no_response","text":"No response was produced."},
	  "stray",
	  {"type":"tool-call","toolCallId":"c3","toolName":"generate_report","result":{"email":"a@b.c","title":"Old"}}
	]`
	parts, err := publicParts([]byte(content))
	if err != nil {
		t.Fatal(err)
	}
	got, err := encodeParts(parts)
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"type":"text","text":"Answer"},` +
		`{"type":"tool-call","toolCallId":"c2","toolName":"save_artifact","args":{"title":"Deck"},"result":{"artifact_id":5,"files":[{"name":"deck.json"}]}},` +
		`{"type":"tool-call","toolCallId":"c3","toolName":"generate_report","result":{"title":"Old"}}]`
	if got != want {
		t.Errorf("public parts:\n got %s\nwant %s", got, want)
	}

	// The oldest rows hold bare text; null and an object hold nothing.
	for content, want := range map[string]string{
		`"Plain [citation:3] text"`: `[{"type":"text","text":"Plain text"}]`,
		`"  spaced  out  "`:         `[{"type":"text","text":"spaced  out"}]`,
		`"[citation:3]"`:            `[]`,
		`null`:                      `[]`,
		`{"type":"text"}`:           `[]`,
	} {
		parts, err := publicParts([]byte(content))
		if err != nil {
			t.Fatalf("%s: %v", content, err)
		}
		if got, _ := encodeParts(parts); got != want {
			t.Errorf("%s: %s, want %s", content, got, want)
		}
	}
	if _, err := publicParts([]byte(`{`)); err == nil {
		t.Error("content that is not JSON was accepted")
	}
}

// TestTheContentHashIsTheOneLinksAlreadyHave pins the hash to the value the
// routes before this package computed for the same messages (Python's
// json.dumps with sorted keys and compact separators, then SHA-256), so a
// thread shared before the move keeps its address when shared again. The
// expected value was produced by that code, not by this.
func TestTheContentHashIsTheOneLinksAlreadyHave(t *testing.T) {
	message := func(id int64, role, content string) publicMessage {
		parts, err := publicParts([]byte(content))
		if err != nil {
			t.Fatal(err)
		}
		return publicMessage{id: id, role: role, parts: parts}
	}
	// Given newest first: the hash orders by id itself.
	messages := []publicMessage{
		message(2, "assistant", `[{"type":"text","text":"Café — done"},{"type":"tool-call","toolName":"save_artifact","toolCallId":"c1","args":{"b":1.5,"a":[1,2]},"result":{"artifact_id":5,"ok":true,"n":null}}]`),
		message(1, "user", `[{"type":"text","text":"hi 😀"}]`),
	}
	const want = "016f0417dbf1a02236a431f8081f2b382184fc2e8e24750550031616f8f3f328"
	got, err := contentHash(messages)
	if err != nil || got != want {
		t.Fatalf("hash = %s, %v; want %s", got, err, want)
	}
	// The order of keys inside a part is not content.
	reordered := []publicMessage{
		message(1, "user", `[{"text":"hi 😀","type":"text"}]`),
		message(2, "assistant", `[{"text":"Café — done","type":"text"},{"result":{"n":null,"ok":true,"artifact_id":5},"args":{"a":[1,2],"b":1.5},"toolCallId":"c1","toolName":"save_artifact","type":"tool-call"}]`),
	}
	if again, _ := contentHash(reordered); again != want {
		t.Errorf("the same content with its keys in another order hashed to %s", again)
	}
	// One changed character is.
	messages[1] = message(1, "user", `[{"type":"text","text":"hi"}]`)
	if changed, _ := contentHash(messages); changed == want {
		t.Error("changed content kept its hash")
	}
}

func TestReadingHowLongALinkLives(t *testing.T) {
	for _, c := range []struct {
		name, body string
		days       int
		expires    bool
		refused    bool
	}{
		{"no body is seven days", "", 7, true, false},
		{"an empty object is seven days", "{}", 7, true, false},
		{"seven", `{"expires_in_days":7}`, 7, true, false},
		{"thirty", `{"expires_in_days": 30}`, 30, true, false},
		{"null is never", `{"expires_in_days":null}`, 0, false, false},
		{"another number is refused, not rounded", `{"expires_in_days":14}`, 0, false, true},
		{"zero is not never", `{"expires_in_days":0}`, 0, false, true},
		{"a string is refused", `{"expires_in_days":"7"}`, 0, false, true},
		{"a list is not a body", `[7]`, 0, false, true},
	} {
		days, expires, failures := readExpiry([]byte(c.body))
		if (len(failures) > 0) != c.refused || days != c.days || expires != c.expires {
			t.Errorf("%s: days %d, expires %v, failures %v", c.name, days, expires, failures)
		}
	}
}

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
	if len(failures) > 0 || request.Title != "New Chat" || request.Archived {
		t.Errorf("defaults: %+v, %v", request, failures)
	}
	request, failures = readCreate([]byte(`{"title":"  Cell \n biology ","archived":true}`))
	if len(failures) > 0 || request.Title != "Cell biology" || !request.Archived {
		t.Errorf("a full body: %+v, %v", request, failures)
	}
	if request, _ = readCreate([]byte(`{"title":"   "}`)); request.Title != "New Chat" {
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
			Tools:    []Tool{{Name: "search_knowledge_base", Description: "Search the documents", EnabledByDefault: true}},
			StopWait: time.Second, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: time.Now,
		}
	}
	if err := whole().Check(); err != nil {
		t.Fatalf("a complete service: %v", err)
	}
	for name, breakIt := range map[string]func(*Service){
		"no database":           func(s *Service) { s.Store.Pool = nil },
		"no registry":           func(s *Service) { s.Turns = nil },
		"no Forget":             func(s *Service) { s.Forget = nil },
		"no tool list":          func(s *Service) { s.Tools = nil },
		"no wait":               func(s *Service) { s.StopWait = 0 },
		"no logger":             func(s *Service) { s.Logger = nil },
		"no clock":              func(s *Service) { s.Now = nil },
		"a tool without a name": func(s *Service) { s.Tools = []Tool{{Description: "x"}} },
		"a tool listed twice":   func(s *Service) { s.Tools = append(s.Tools, s.Tools[0]) },
	} {
		service := whole()
		breakIt(service)
		if err := service.Check(); err == nil {
			t.Errorf("%s: the service started", name)
		}
	}
	// A deployment with no tools to pick lists none; that is not "unset".
	empty := whole()
	empty.Tools = []Tool{}
	if err := empty.Check(); err != nil {
		t.Errorf("an empty tool list: %v", err)
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
		"POST /api/v1/threads/{thread_id}/snapshots",
	}
	if got := strings.Join(BodyRoutes(), "\n"); got != strings.Join(want, "\n") {
		t.Errorf("routes that read a body:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
	if len(seen) != 16 {
		t.Errorf("%d routes, want 16", len(seen))
	}
}

func TestALinksTokenIsLongAndNeverTheSame(t *testing.T) {
	first, err := newToken()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := newToken()
	// 64 characters is the column's width and 48 bytes of randomness.
	if len(first) != 64 || first == second || strings.ContainsAny(first, "+/=") {
		t.Errorf("tokens %q and %q", first, second)
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
