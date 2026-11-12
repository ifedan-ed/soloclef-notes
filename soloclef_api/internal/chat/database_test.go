package chat

import (
	"context"
	"encoding/json"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/notebookscope"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	agentpostgres "git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint/postgres"
	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/deliverables"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/websearch"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// databaseVariable names a throwaway Postgres at this tier's schema (a
// pgvector/pgvector:pg17 brought up to date by soloclef_schema's
// `migrate up`). The test **truncates the tables it seeds**, so it must
// never name a database anyone uses. With it unset the test is skipped.
//
//	SOLOCLEF_API_TEST_CHAT_DATABASE=postgres://check:check@127.0.0.1:39817/check \
//	  go test ./internal/chat/ -run OverARealDatabase -count=1 -v
const databaseVariable = "SOLOCLEF_API_TEST_CHAT_DATABASE"

// seed is a small world of three accounts.
//
// Ada has standing instructions and a memory, threads 1 and 2, a folder,
// three documents with their chunks, two connected search servers, and two
// notebooks (30 with sources, 31 with nothing ticked) whose chats are
// threads 3 and 4. Dee is somebody else: a thread, a document and its
// chunk, a notebook and an Exa key of her own, none of which may ever be
// found from Ada's account, nor Ada's from hers. Ben has an Exa connector
// he switched off, and nothing else.
const seed = `
TRUNCATE "user", site_settings, soloclef_agent_checkpoints CASCADE;
INSERT INTO "user" (id, email, display_name, memory_md, chat_instructions, citations_enabled) VALUES
  ('aaaaaaaa-0000-4000-8000-000000000001', 'ada@example.org', 'Ada Lovelace', 'Prefers metric units.', 'Answer briefly.', true),
  ('bbbbbbbb-0000-4000-8000-000000000002', 'ben@example.org', NULL, '', '', true),
  ('dddddddd-0000-4000-8000-000000000004', 'dee@example.org', 'Dee', 'Dee''s own memory.', 'Answer at length.', false);
INSERT INTO new_chat_threads (id, title, updated_at, user_id) VALUES
  (1, 'New chat', '2020-01-01', 'aaaaaaaa-0000-4000-8000-000000000001'),
  (2, 'Another', now(), 'aaaaaaaa-0000-4000-8000-000000000001'),
  (9, 'Dee''s chat', '2020-01-01', 'dddddddd-0000-4000-8000-000000000004');
INSERT INTO folders (id, name, position, user_id) VALUES
  (5, 'Biology', 'a0', 'aaaaaaaa-0000-4000-8000-000000000001'),
  (8, 'Dee''s folder', 'a0', 'dddddddd-0000-4000-8000-000000000004');
INSERT INTO documents (id, title, document_type, document_metadata, content, content_hash, user_id, folder_id, status) VALUES
  (12, 'Cell biology', 'FILE', '{"url": "https://www.example.org/cells"}', 'x', 'h12', 'aaaaaaaa-0000-4000-8000-000000000001', 5, '{"state": "ready"}'),
  (13, 'Being removed', 'NOTE', '{}', 'x', 'h13', 'aaaaaaaa-0000-4000-8000-000000000001', NULL, '{"state": "deleting"}'),
  (14, 'Genetics', 'FILE', '{}', 'x', 'h14', 'aaaaaaaa-0000-4000-8000-000000000001', 5, '{"state": "ready"}'),
  (90, 'Private elsewhere', 'FILE', '{}', 'x', 'h90', 'dddddddd-0000-4000-8000-000000000004', 8, '{"state": "ready"}');
INSERT INTO chunks (id, content, "position", page_start, document_id, user_id) VALUES
  (481, 'Mitosis has four phases: prophase, metaphase, anaphase and telophase.', 0, 3, 12, 'aaaaaaaa-0000-4000-8000-000000000001'),
  (482, 'Meiosis halves the chromosome number.', 1, NULL, 12, 'aaaaaaaa-0000-4000-8000-000000000001'),
  (483, 'Mitosis notes that are being deleted.', 0, NULL, 13, 'aaaaaaaa-0000-4000-8000-000000000001'),
  (900, 'Mitosis as another account wrote about it.', 0, NULL, 90, 'dddddddd-0000-4000-8000-000000000004');
INSERT INTO search_source_connectors (id, name, connector_type, config, user_id, enabled) VALUES
  (1, 'Some tools', 'MCP_CONNECTOR', '{"server_config": {"url": "https://tools.example.com/mcp", "headers": {"x-api-key": "not-exa"}}}', 'aaaaaaaa-0000-4000-8000-000000000001', true),
  (2, 'Exa', 'MCP_CONNECTOR', '{"server_config": {"url": "https://mcp.exa.ai/mcp", "headers": {"x-api-key": "exa-ada"}}}', 'aaaaaaaa-0000-4000-8000-000000000001', true),
  (3, 'Exa', 'MCP_CONNECTOR', '{"server_config": {"url": "https://mcp.exa.ai/mcp", "headers": {"x-api-key": "exa-dee"}}}', 'dddddddd-0000-4000-8000-000000000004', true),
  (4, 'Exa', 'MCP_CONNECTOR', '{"server_config": {"url": "https://mcp.exa.ai/mcp", "headers": {"x-api-key": "exa-ben-off"}}}', 'bbbbbbbb-0000-4000-8000-000000000002', false);
INSERT INTO notebooks (id, user_id, title, chat_goal, response_length) VALUES
  (30, 'aaaaaaaa-0000-4000-8000-000000000001', 'Cells', 'learning_guide', 'shorter'),
  (31, 'aaaaaaaa-0000-4000-8000-000000000001', 'Empty', 'default', 'default'),
  (80, 'dddddddd-0000-4000-8000-000000000004', 'Dee''s notebook', 'default', 'default');
INSERT INTO notebook_sources (notebook_id, user_id, document_id, folder_id, enabled, added_at) VALUES
  (30, 'aaaaaaaa-0000-4000-8000-000000000001', 12, NULL, true, '2026-01-01'),
  (30, 'aaaaaaaa-0000-4000-8000-000000000001', 13, NULL, false, '2026-01-02'),
  (30, 'aaaaaaaa-0000-4000-8000-000000000001', NULL, 5, true, '2026-01-03'),
  (31, 'aaaaaaaa-0000-4000-8000-000000000001', 12, NULL, false, '2026-01-01'),
  (80, 'dddddddd-0000-4000-8000-000000000004', 90, NULL, true, '2026-01-01');
INSERT INTO new_chat_threads (id, title, user_id, notebook_id) VALUES
  (3, 'In a notebook', 'aaaaaaaa-0000-4000-8000-000000000001', 30),
  (4, 'In an empty notebook', 'aaaaaaaa-0000-4000-8000-000000000001', 31),
  (10, 'In Dee''s notebook', 'dddddddd-0000-4000-8000-000000000004', 80);
SELECT setval('new_chat_messages_id_seq', 1000);
SELECT setval('documents_id_seq', 1000);
`

// TestTheStoresOverARealDatabase asks the generated statements what the
// in-memory tests ask stand-ins: that they find and write the rows they are
// meant to, with the real constraints, enum casts and indexes underneath.
func TestTheStoresOverARealDatabase(t *testing.T) {
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	defer pool.Close()
	// The checkpoint table is the migrations' own; nothing is created here.
	checkpoints, err := agentpostgres.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, seed); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	store := Store{Pool: pool}

	t.Run("a thread is its owner's and nobody else's", func(t *testing.T) {
		thread, err := store.Thread(ctx, ada, 1)
		if err != nil || thread == nil || thread.ID != 1 || thread.UserID != ada {
			t.Fatalf("thread = %+v, %v", thread, err)
		}
		// Another account's thread and one that does not exist are the same
		// answer, in both directions.
		for name, ask := range map[string]struct {
			account string
			thread  int64
		}{"Ada's, asked as Dee": {dee, 1}, "Ada's, asked as Ben": {ben, 1}, "Dee's, asked as Ada": {ada, 9}, "no thread": {ada, 404}} {
			if absent, err := store.Thread(ctx, ask.account, ask.thread); absent != nil || err != nil {
				t.Errorf("%s: %+v, %v", name, absent, err)
			}
		}
		// The same account, spelled in capitals by its token.
		if thread, err := store.Thread(ctx, strings.ToUpper(ada), 1); thread == nil || err != nil {
			t.Errorf("the owner is not recognised when the id's case differs: %+v, %v", thread, err)
		}
		about, err := store.Context(ctx, Turn{ThreadID: 1, UserID: ada})
		if err != nil || about.Instructions != "Answer briefly." || !about.Citations || about.Notebook != nil {
			t.Errorf("context = %+v, %v", about, err)
		}
		if about.UserName != "Ada Lovelace" || about.UserMemory != "Prefers metric units." {
			t.Errorf("the asker = %q, memory %q", about.UserName, about.UserMemory)
		}
		// The instructions, the citations switch and the memory are the
		// account's own: Dee's turn is built from Dee's.
		dees, err := store.Context(ctx, Turn{ThreadID: 9, UserID: dee})
		if err != nil || dees.Instructions != "Answer at length." || dees.Citations || dees.UserMemory != "Dee's own memory." || dees.UserName != "Dee" {
			t.Errorf("Dee's context = %+v, %v", dees, err)
		}
		// A thread is read with its account: the same id asked about by
		// another account is an error, not that thread's context, and never
		// the other account's instructions or memory.
		for name, other := range map[string]Turn{"Dee on Ada's thread": {ThreadID: 1, UserID: dee}, "Ada on Dee's thread": {ThreadID: 9, UserID: ada}, "Ada on Dee's notebook chat": {ThreadID: 10, UserID: ada}} {
			if leaked, err := store.Context(ctx, other); err == nil {
				t.Errorf("%s was read: %+v", name, leaked)
			}
		}
	})

	t.Run("a notebook chat's limit", func(t *testing.T) {
		// Thread 3 belongs to notebook 30, whose sources are: document 12
		// (enabled), document 13 (unticked) and folder 5 (enabled; it holds
		// 12 and 14). A source from another account cannot be linked at
		// all: the schema's composite keys refuse the row, whichever
		// account it claims to be of.
		for _, claimed := range []string{ada, dee} {
			if _, err := pool.Exec(ctx, `INSERT INTO notebook_sources (notebook_id, user_id, document_id) VALUES (30, $1, 90)`, claimed); err == nil {
				t.Fatalf("Dee's document was linked into Ada's notebook as %s's", claimed)
			}
			if _, err := pool.Exec(ctx, `INSERT INTO notebook_sources (notebook_id, user_id, folder_id) VALUES (30, $1, 8)`, claimed); err == nil {
				t.Fatalf("Dee's folder was linked into Ada's notebook as %s's", claimed)
			}
		}
		about, err := store.Context(ctx, Turn{ThreadID: 3, UserID: ada})
		if err != nil || about.Notebook == nil {
			t.Fatalf("context = %+v, %v", about, err)
		}
		notebook := about.Notebook
		if got := notebook.DocumentIDs; len(got) != 2 || got[0] != 12 || got[1] != 14 {
			t.Errorf("the limit = %v, want [12 14]: the ticked document, the folder's documents, nothing unticked and nothing of another account", got)
		}
		if got := strings.Join(notebook.SourceTitles, "|"); got != "Cell biology|Biology/" {
			t.Errorf("the listed sources = %q", got)
		}
		if notebook.Title != "Cells" || notebook.Goal != "learning_guide" || notebook.Length != "shorter" {
			t.Errorf("the notebook = %+v", notebook)
		}
		// Thread 4's notebook has nothing ticked: a notebook chat with an
		// empty limit, which is not the same as no limit.
		empty, err := store.Context(ctx, Turn{ThreadID: 4, UserID: ada})
		if err != nil || empty.Notebook == nil || len(empty.Notebook.DocumentIDs) != 0 || len(empty.Notebook.SourceTitles) != 0 {
			t.Fatalf("an empty notebook's context = %+v, %v", empty.Notebook, err)
		}
		// The statements that read a notebook's sources hold the limit by
		// themselves: asked about Dee's notebook as Ada they find nothing,
		// so a notebook id from elsewhere can never widen a search.
		if sources, err := notebookscope.Read(ctx, pool, ada, 80); len(sources.Titles) != 0 || len(sources.DocumentIDs) != 0 || err != nil {
			t.Errorf("Ada read the sources of Dee's notebook: %+v, %v", sources, err)
		}
		if sources, err := notebookscope.Read(ctx, pool, dee, 30); len(sources.Titles) != 0 || len(sources.DocumentIDs) != 0 || err != nil {
			t.Errorf("Dee read the sources of Ada's notebook: %+v, %v", sources, err)
		}
		// And a notebook's settings are its owner's to read.
		if _, found, err := notebookscope.SettingsOf(ctx, pool, ada, 80); found || err != nil {
			t.Errorf("Ada read the settings of Dee's notebook: %v, %v", found, err)
		}
		if settings, found, err := notebookscope.SettingsOf(ctx, pool, dee, 80); !found || err != nil || settings.Title == "" {
			t.Errorf("Dee's own notebook settings: %+v, %v, %v", settings, found, err)
		}
		// Dee's own notebook chat is limited to Dee's own document.
		dees, err := store.Context(ctx, Turn{ThreadID: 10, UserID: dee})
		if err != nil || dees.Notebook == nil || len(dees.Notebook.DocumentIDs) != 1 || dees.Notebook.DocumentIDs[0] != 90 {
			t.Errorf("Dee's notebook chat = %+v, %v", dees.Notebook, err)
		}
	})

	t.Run("an account's chat model", func(t *testing.T) {
		choices := StoredChoices{Pool: pool}
		if name, err := choices.ChatModel(ctx, ada); err != nil || name != "" {
			t.Fatalf("before any choice: %q, %v", name, err)
		}
		for _, name := range []string{"model-a", "model-b"} {
			if err := choices.ChooseChatModel(ctx, ada, name); err != nil {
				t.Fatal(err)
			}
		}
		if name, _ := choices.ChatModel(ctx, ada); name != "model-b" {
			t.Errorf("the choice = %q, want the later one", name)
		}
		// One row per account: a second choice replaced the first.
		if n := countRows(t, pool, "user_models"); n != 1 {
			t.Errorf("%d rows after two choices by one account", n)
		}
		// Another account neither reads this one's choice nor changes it.
		if name, _ := choices.ChatModel(ctx, dee); name != "" {
			t.Errorf("another account reads this one's choice: %q", name)
		}
		if err := choices.ChooseChatModel(ctx, dee, "model-dee"); err != nil {
			t.Fatal(err)
		}
		if err := choices.ChooseChatModel(ctx, ben, ""); err != nil {
			t.Fatal(err)
		}
		if name, _ := choices.ChatModel(ctx, ada); name != "model-b" {
			t.Errorf("Ada's choice after Dee chose and Ben cleared: %q", name)
		}
		if err := choices.ChooseChatModel(ctx, ada, ""); err != nil {
			t.Fatal(err)
		}
		if name, _ := choices.ChatModel(ctx, ada); name != "" {
			t.Errorf("a cleared choice is still %q", name)
		}
		if name, _ := choices.ChatModel(ctx, dee); name != "model-dee" {
			t.Errorf("clearing Ada's choice changed Dee's: %q", name)
		}
	})

	t.Run("asking a question again", func(t *testing.T) {
		// Thread 2 has two turns, the second answered in two stretches (it
		// paused): q1 a1 q2 a2 a2b, and a public snapshot of a2.
		if _, err := pool.Exec(ctx, `
INSERT INTO new_chat_messages (id, role, content, thread_id, turn_id, created_at) VALUES
  (201, 'USER', '[{"type":"text","text":"first"}]', 2, '2:1', '2026-01-01 10:00:00+00'),
  (202, 'ASSISTANT', '[{"type":"text","text":"one"}]', 2, '2:1', '2026-01-01 10:00:00+00'),
  (203, 'USER', '[{"type":"text","text":"second"}]', 2, '2:2', '2026-01-01 10:01:00+00'),
  (204, 'ASSISTANT', '[{"type":"text","text":"two"}]', 2, '2:2', '2026-01-01 10:01:00+00'),
  (205, 'ASSISTANT', '[{"type":"text","text":"two, resumed"}]', 2, '2:3', '2026-01-01 10:02:00+00');
INSERT INTO public_chat_snapshots (id, thread_id, user_id, share_token, content_hash, snapshot_data, message_ids) VALUES
  (1, 2, 'aaaaaaaa-0000-4000-8000-000000000001', 'tok-a', 'h', '{}', '{201,202}'),
  (2, 2, 'aaaaaaaa-0000-4000-8000-000000000001', 'tok-b', 'h2', '{}', '{203,204}');
INSERT INTO token_usage (prompt_tokens, completion_tokens, total_tokens, cost_micros, usage_type, thread_id, message_id, user_id)
  VALUES (5, 5, 10, 0, 'chat', 2, 204, 'aaaaaaaa-0000-4000-8000-000000000001');`); err != nil {
			t.Fatal(err)
		}
		latest, err := store.Question(ctx, 2, 0)
		if err != nil || latest == nil || latest.ID != 203 || latest.TurnID != "2:2" || len(latest.Replaced) != 3 {
			t.Fatalf("the latest question = %+v, %v", latest, err)
		}
		// The resumed stretch's answer has a turn id of its own; it stands
		// for the question before it.
		if resumed, err := store.Question(ctx, 2, 205); err != nil || resumed == nil || resumed.ID != 203 {
			t.Errorf("the question of a resumed answer = %+v, %v", resumed, err)
		}
		if first, err := store.Question(ctx, 2, 202); err != nil || first == nil || first.ID != 201 || len(first.Replaced) != 5 {
			t.Errorf("the question of the first answer = %+v, %v", first, err)
		}
		if _, err := store.Question(ctx, 2, 1001); err != errNotInThread {
			t.Errorf("a message of another thread: %v", err)
		}
		before, err := store.HistoryBefore(ctx, 2, latest)
		if err != nil || len(before) != 2 || before[0].Assistant || !before[1].Assistant {
			t.Fatalf("the history before the question = %+v, %v", before, err)
		}
		opened, err := store.ReplaceTurn(ctx, latest, NewTurn{ThreadID: 2, UserID: ada, TurnID: "2:9", UserContent: []byte(`[{"type":"text","text":"second, reworded"}]`), AssistantContent: emptyAnswer})
		if err != nil || opened.UserMessageID == 0 || opened.AssistantMessageID == 0 {
			t.Fatalf("replace = %+v, %v", opened, err)
		}
		var ids []int64
		rows, _ := pool.Query(ctx, `SELECT id FROM new_chat_messages WHERE thread_id = 2 ORDER BY created_at, id`)
		for rows.Next() {
			var id int64
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		if len(ids) != 4 || ids[0] != 201 || ids[1] != 202 || ids[2] != opened.UserMessageID || ids[3] != opened.AssistantMessageID {
			t.Errorf("the thread's messages = %v", ids)
		}
		// The snapshot that showed a replaced answer is gone; the other
		// stays. What the replaced answer cost is still counted.
		var snapshots, usage int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM public_chat_snapshots WHERE thread_id = 2`).Scan(&snapshots)
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM token_usage WHERE thread_id = 2 AND message_id IS NULL AND total_tokens = 10`).Scan(&usage)
		if snapshots != 1 || usage != 1 {
			t.Errorf("snapshots left = %d (want 1), usage rows kept = %d (want 1)", snapshots, usage)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM new_chat_messages WHERE thread_id = 2`); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a turn's rows", func(t *testing.T) {
		question, _ := userContent("What are the phases?", nil)
		opened, err := store.OpenTurn(ctx, NewTurn{ThreadID: 1, UserID: ada, TurnID: "1:1700000000000", UserContent: question, AssistantContent: emptyAnswer})
		if err != nil || opened.UserMessageID == 0 || opened.AssistantMessageID != opened.UserMessageID+1 {
			t.Fatalf("opened = %+v, %v", opened, err)
		}
		var responding *string
		var touched bool
		_ = pool.QueryRow(ctx, `SELECT ai_responding_to_user_id::text FROM chat_session_state WHERE thread_id = 1`).Scan(&responding)
		_ = pool.QueryRow(ctx, `SELECT updated_at > '2025-01-01' FROM new_chat_threads WHERE id = 1`).Scan(&touched)
		if responding == nil || *responding != ada || !touched {
			t.Errorf("responding = %v, thread touched = %v", responding, touched)
		}
		// The same turn id again is refused by the table, and writes nothing.
		if _, err := store.OpenTurn(ctx, NewTurn{ThreadID: 1, UserID: ada, TurnID: "1:1700000000000", UserContent: question, AssistantContent: emptyAnswer}); err == nil {
			t.Error("a turn id was used twice on one thread")
		}
		if n, _ := store.AssistantMessages(ctx, 1); n != 1 {
			t.Errorf("%d answers after a refused duplicate", n)
		}

		if err := store.StoreContent(ctx, opened.AssistantMessageID, []byte(`[{"type":"text","text":"Four"}]`)); err != nil {
			t.Fatal(err)
		}
		usage := &Usage{PromptTokens: 250, CompletionTokens: 30, TotalTokens: 280, Breakdown: []byte(`{"m":{"total_tokens":280}}`), Calls: []byte(`{"calls":[]}`)}
		closed := ClosedTurn{ThreadID: 1, MessageID: opened.AssistantMessageID, UserID: ada,
			Content: []byte(`[{"type":"text","text":"Four phases [citation:481]."}]`), Usage: usage}
		if err := store.CloseTurn(ctx, closed); err != nil {
			t.Fatal(err)
		}
		// Closing twice writes one usage row: the answer is not billed twice.
		if err := store.CloseTurn(ctx, closed); err != nil {
			t.Fatal(err)
		}
		var usageRows, total int
		var kind string
		_ = pool.QueryRow(ctx, `SELECT count(*), max(total_tokens), max(usage_type) FROM token_usage WHERE message_id = $1`, opened.AssistantMessageID).Scan(&usageRows, &total, &kind)
		if usageRows != 1 || total != 280 || kind != "chat" {
			t.Errorf("usage rows = %d, total %d, type %q", usageRows, total, kind)
		}
		_ = pool.QueryRow(ctx, `SELECT ai_responding_to_user_id::text FROM chat_session_state WHERE thread_id = 1`).Scan(&responding)
		if responding != nil {
			t.Errorf("still responding to %v after the turn closed", *responding)
		}

		// A resumed turn: an answer row and no question.
		resumed, err := store.OpenTurn(ctx, NewTurn{ThreadID: 1, UserID: ada, TurnID: "1:1700000000001", AssistantContent: emptyAnswer})
		if err != nil || resumed.UserMessageID != 0 || resumed.AssistantMessageID == 0 {
			t.Fatalf("resumed = %+v, %v", resumed, err)
		}
		history, err := store.History(ctx, 1)
		if err != nil || len(history) != 3 || history[0].Assistant || !history[1].Assistant || textOf(history[1].Content) != "Four phases [citation:481]." {
			t.Fatalf("history = %+v, %v", history, err)
		}
		if latest, _ := store.LatestAssistantMessage(ctx, 1); latest != resumed.AssistantMessageID {
			t.Errorf("latest answer = %d, want %d", latest, resumed.AssistantMessageID)
		}
		if latest, err := store.LatestAssistantMessage(ctx, 2); latest != 0 || err != nil {
			t.Errorf("a thread with no answers: %d, %v", latest, err)
		}
		if err := store.SetTitle(ctx, ada, 1, "Cell division"); err != nil {
			t.Fatal(err)
		}
		// Named as another account, the write finds no thread and changes
		// nothing.
		if err := store.SetTitle(ctx, dee, 1, "Dee was here"); err != nil {
			t.Fatal(err)
		}
		var title string
		_ = pool.QueryRow(ctx, `SELECT title FROM new_chat_threads WHERE id = 1`).Scan(&title)
		if title != "Cell division" {
			t.Errorf("title = %q", title)
		}
		// What the answer cost is recorded for the account that asked.
		var billed string
		_ = pool.QueryRow(ctx, `SELECT user_id::text FROM token_usage WHERE message_id = $1`, opened.AssistantMessageID).Scan(&billed)
		if billed != ada {
			t.Errorf("the usage row is %s's, want Ada's", billed)
		}
	})

	t.Run("always allow is a row per account, written once", func(t *testing.T) {
		if trusted, err := store.Trusted(ctx, ada, "write_note"); trusted || err != nil {
			t.Fatalf("trusted before anything: %v, %v", trusted, err)
		}
		for i := 0; i < 2; i++ {
			if err := store.Trust(ctx, ada, "write_note"); err != nil {
				t.Fatal(err)
			}
		}
		var count int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM agent_permission_rules WHERE permission = 'write_note'`).Scan(&count)
		if count != 1 {
			t.Errorf("%d rules after trusting twice", count)
		}
		if trusted, _ := store.Trusted(ctx, ada, "write_note"); !trusted {
			t.Error("not trusted after trusting")
		}
		for name, check := range map[string]func() (bool, error){
			"another account": func() (bool, error) { return store.Trusted(ctx, ben, "write_note") },
			"a third account": func() (bool, error) { return store.Trusted(ctx, dee, "write_note") },
			"another tool":    func() (bool, error) { return store.Trusted(ctx, ada, "delete_note") },
		} {
			if trusted, err := check(); trusted || err != nil {
				t.Errorf("%s is trusted: %v, %v", name, trusted, err)
			}
		}
		// A rule that says "ask" or covers one thread is not "always allow".
		_, _ = pool.Exec(ctx, `INSERT INTO agent_permission_rules (user_id, thread_id, permission, pattern, action) VALUES
			($1, NULL, 'send_mail', '*', 'ask'), ($1, 1, 'move_file', '*', 'allow')`, ada)
		for _, name := range []string{"send_mail", "move_file"} {
			if trusted, _ := store.Trusted(ctx, ada, name); trusted {
				t.Errorf("%s is trusted by a rule that does not say so", name)
			}
		}
		// Removed in settings: gone from the next question.
		_, _ = pool.Exec(ctx, `DELETE FROM agent_permission_rules WHERE permission = 'write_note'`)
		if trusted, _ := store.Trusted(ctx, ada, "write_note"); trusted {
			t.Error("still trusted after the rule was removed")
		}
	})

	t.Run("the document search's rows", func(t *testing.T) {
		rows := documents.Store{Pool: pool}
		ranks, err := rows.KeywordRanks(ctx, ada, "mitosis phases", nil, 200)
		if err != nil || len(ranks) != 1 || ranks[0].ChunkID != 481 || ranks[0].Rank != 1 {
			t.Fatalf("keyword ranks = %+v, %v: only the ready document of this account matches both words", ranks, err)
		}
		if wide, _ := rows.KeywordRanks(ctx, ada, "mitosis", nil, 200); len(wide) != 1 {
			t.Errorf("'mitosis' found %+v: the deleting document's chunk and the other account's must not be there", wide)
		}
		if scoped, _ := rows.KeywordRanks(ctx, ada, "mitosis", []int64{13}, 200); len(scoped) != 0 {
			t.Errorf("a scope of one deleting document found %+v", scoped)
		}
		// The same words asked by Dee find Dee's chunk and none of Ada's;
		// asked by Ben, who has no documents, nothing; and a scope that
		// names Dee's document from Ada's account finds nothing.
		if theirs, _ := rows.KeywordRanks(ctx, dee, "mitosis", nil, 200); len(theirs) != 1 || theirs[0].ChunkID != 900 {
			t.Errorf("Dee's 'mitosis' found %+v, want only her own chunk 900", theirs)
		}
		if none, _ := rows.KeywordRanks(ctx, ben, "mitosis", nil, 200); len(none) != 0 {
			t.Errorf("Ben, who has no documents, found %+v", none)
		}
		if crossed, _ := rows.KeywordRanks(ctx, ada, "mitosis", []int64{90}, 200); len(crossed) != 0 {
			t.Errorf("Ada scoped to Dee's document found %+v", crossed)
		}
		// Ids from anywhere, as a vector store with a bad filter might
		// answer them: only this account's ready chunks come back.
		chunks, err := rows.Chunks(ctx, ada, []int64{900, 483, 482, 481, 12345}, nil)
		if err != nil || len(chunks) != 2 || chunks[0].ID != 481 || chunks[1].ID != 482 {
			t.Fatalf("chunks = %+v, %v", chunks, err)
		}
		first := chunks[0]
		if first.Title != "Cell biology" || first.DocumentType != "FILE" || first.Page == nil || *first.Page != 3 || first.DocumentID != 12 || !strings.Contains(string(first.Metadata), "example.org") {
			t.Errorf("chunk = %+v", first)
		}
		if theirs, _ := rows.Chunks(ctx, dee, []int64{900, 483, 482, 481}, nil); len(theirs) != 1 || theirs[0].ID != 900 {
			t.Errorf("Dee, handed Ada's chunk ids, read %+v", theirs)
		}
		if scoped, _ := rows.Chunks(ctx, ada, []int64{481, 482}, []int64{99}); len(scoped) != 0 {
			t.Errorf("chunks outside the scope came back: %+v", scoped)
		}
		if ids, _ := rows.ChunkIDsOfDocuments(ctx, ada, []int64{12, 90}); len(ids) != 2 || ids[0] != 481 || ids[1] != 482 {
			t.Errorf("chunk ids of the scope = %v: document 90 is another account's", ids)
		}
		if ids, _ := rows.DocumentsInFolders(ctx, ada, []int64{5, 8}); len(ids) != 2 || ids[0] != 12 || ids[1] != 14 {
			t.Errorf("documents in the folders = %v: folder 8 is another account's", ids)
		}
		if ids, _ := rows.DocumentsInFolders(ctx, dee, []int64{5}); len(ids) != 0 {
			t.Errorf("another account read the folder: %v", ids)
		}
	})

	t.Run("a search provider's key is the account's own", func(t *testing.T) {
		keys := websearch.StoredKeys{Pool: pool}
		if key, found, err := keys.Key(ctx, ada, websearch.Exa); err != nil || !found || key != "exa-ada" {
			t.Errorf("Ada's Exa key = %q, %v, %v", key, found, err)
		}
		if _, found, _ := keys.Key(ctx, ada, websearch.Tavily); found {
			t.Error("a Tavily key was found where none is connected")
		}
		if key, _, _ := keys.Key(ctx, dee, websearch.Exa); key != "exa-dee" {
			t.Errorf("Dee's Exa key = %q", key)
		}
		// A connector switched off keeps its key and is left alone.
		if key, found, err := keys.Key(ctx, ben, websearch.Exa); found || key != "" || err != nil {
			t.Errorf("Ben's switched-off Exa key was used: %q, %v, %v", key, found, err)
		}
	})

	t.Run("an artifact is saved as rows the artifact routes read", func(t *testing.T) {
		local, err := filestore.OpenLocal(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		var indexed []int64
		saver := deliverables.Saver{
			Rows: deliverables.Store{Pool: pool}, Files: local, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
			NewID: func() string { return "00000000000000000000000000000abc" },
			Index: func(_ context.Context, documentID int64, _ string) error {
				indexed = append(indexed, documentID)
				return nil
			},
		}
		cards := make([]deliverables.Card, 5)
		for i := range cards {
			cards[i] = deliverables.Card{Front: "Q" + string(rune('1'+i)), Back: "A"}
		}
		deck, err := deliverables.NewDeck("Mitosis", cards)
		if err != nil {
			t.Fatal(err)
		}
		owner := deliverables.Owner{ThreadID: 1, UserID: ada}
		saved, err := saver.SaveFlashcards(ctx, owner, "d1/s1", deck)
		if err != nil {
			t.Fatalf("saving: %v", err)
		}
		// What internal/artifacts' list and manifest read.
		var format, title, status, path, docType, creator, docOwner, metadata, toolCall string
		var generation, thread int
		err = pool.QueryRow(ctx, `
			SELECT a.format, a.generation, a.thread_id, a.user_id::text, a.metadata::text, a.created_by_tool_call_id,
			       d.title, d.status ->> 'state', d.path, d.document_type::text, d.user_id::text
			FROM artifacts a JOIN documents d ON d.id = a.document_id
			WHERE a.id = $1 AND (d.document_metadata ->> 'artifact_id')::int = a.id`, saved.ArtifactID).
			Scan(&format, &generation, &thread, &creator, &metadata, &toolCall, &title, &status, &path, &docType, &docOwner)
		if err != nil {
			t.Fatalf("reading the artifact back: %v", err)
		}
		if format != "flashcards" || generation != 1 || thread != 1 || creator != ada || toolCall != "d1/s1" || title != "Mitosis" ||
			status != "pending" || path != "/documents/Mitosis.md" || docType != "ARTIFACT" || docOwner != ada || !strings.Contains(metadata, `"verified": true`) {
			t.Errorf("artifact row: %s gen %d thread %d by %s (%s) %q %s %s %s owner %s %s", format, generation, thread, creator, toolCall, title, status, path, docType, docOwner, metadata)
		}
		var key, backend, mime, checksum string
		var size int64
		_ = pool.QueryRow(ctx, `SELECT storage_key, storage_backend, mime_type, size_bytes, checksum_sha256 FROM artifact_files WHERE artifact_id = $1 AND role = 'primary'`, saved.ArtifactID).
			Scan(&key, &backend, &mime, &size, &checksum)
		body, err := local.Open(ctx, key)
		if err != nil {
			t.Fatalf("the file row's key %q is not in the store: %v", key, err)
		}
		stored, _ := io.ReadAll(body)
		body.Close()
		if backend != "local" || mime != "application/json" || size != int64(len(stored)) || len(checksum) != 64 || saved.Files[0].SizeBytes != len(stored) {
			t.Errorf("file row: %s %s %d %s for %d bytes", backend, mime, size, checksum, len(stored))
		}
		if len(indexed) != 1 {
			t.Errorf("indexing was asked for %v", indexed)
		}
		// A second deck of the same title gets the next name, not a clash.
		again, err := saver.SaveFlashcards(ctx, owner, "d2/s1", deck)
		if err != nil {
			t.Fatalf("a second deck of the same title: %v", err)
		}
		_ = pool.QueryRow(ctx, `SELECT d.path FROM artifacts a JOIN documents d ON d.id = a.document_id WHERE a.id = $1`, again.ArtifactID).Scan(&path)
		if path != "/documents/Mitosis (2).md" {
			t.Errorf("second path = %q", path)
		}
		// A save for an account that is not there leaves nothing behind.
		before := countRows(t, pool, "artifacts")
		if _, err := saver.SaveFlashcards(ctx, deliverables.Owner{ThreadID: 1, UserID: "eeeeeeee-0000-4000-8000-000000000005"}, "x", deck); err == nil {
			t.Error("an artifact was saved for an account that does not exist")
		}
		// One whose file cannot be stored (the key is taken) rolls its rows back.
		clash := saver
		clash.NewID = func() string { return "taken" }
		_ = local.Put(ctx, "artifacts/"+ada+"/"+itoa(again.ArtifactID+1)+"/primary/taken.json", []byte("x"), "")
		if _, err := clash.SaveFlashcards(ctx, owner, "x", deck); err == nil {
			t.Error("a save whose file could not be stored succeeded")
		}
		if after := countRows(t, pool, "artifacts"); after != before {
			t.Errorf("failed saves left rows: %d before, %d after", before, after)
		}
	})

	// A whole turn through the routes, with the real stores underneath: the
	// rows, the checkpoint table, and a pause that another "process" (a
	// second service over the same database) resumes.
	t.Run("a turn, a pause and a resume over the real tables", func(t *testing.T) {
		write := tool.Tool{Name: "write_note", Parameters: objectSchema, Approval: tool.Ask,
			Run: func(_ context.Context, call tool.Call) (string, error) {
				call.Citations.Number(citation.Source{Kind: "kb_chunk", Ref: "482"})
				return "written", nil
			}}
		script := modeltest.NewScript(
			modeltest.Reply{Text: "Noting it.", Calls: []model.ToolCall{{ID: "w1", Name: "write_note", Args: `{"text":"hi"}`}},
				Usage: model.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
			modeltest.Reply{Text: "Noted [1]."},
		)
		wire := func(s *Service, sc *scripted) {
			s.Rows, s.Store, sc.store = store, checkpoints, checkpoints
			sc.rows = nil
		}
		first := newApp(t, script, []tool.Tool{write}, wire, trustFrom(store))
		frames, _ := first.ask(ada, `{"chat_id":2,"user_query":"Note this"}`)
		page := readAll(t, frames)
		if page.failure != nil || len(page.interrupts) != 1 {
			t.Fatalf("failure %v, interrupts %v", page.failure, page.interrupts)
		}
		// The second process's clock reads later than the first's: a turn id
		// carries the millisecond, and the table refuses one used twice.
		later := func(s *Service, _ *scripted) {
			now := s.Now
			s.Now = func() time.Time { return now().Add(time.Hour) }
		}
		second := newApp(t, script, []tool.Tool{write}, wire, trustFrom(store), later)
		// Another account sees no thread, whichever process it asks, and
		// cannot answer the waiting approval.
		if got := second.call(dee, "GET", "/api/v1/threads/2/pending-interrupts", ""); got.status != 404 {
			t.Fatalf("Dee read what waits in Ada's thread: %d %s", got.status, got.raw)
		}
		if got := second.call(dee, "POST", "/api/v1/threads/2/resume", `{"decisions":[{"type":"approve","tool_call_id":"w1"}]}`); got.status != 404 {
			t.Fatalf("Dee answered Ada's approval: %d %s", got.status, got.raw)
		}
		pending := second.call(ada, "GET", "/api/v1/threads/2/pending-interrupts", "")
		if list, _ := pending.body["pending_interrupts"].([]any); len(list) != 1 || pending.body["assistant_message_id"] != page.assistantMessageID {
			t.Fatalf("pending, asked of another process: %s", pending.raw)
		}
		response := second.request(ada, "POST", "/api/v1/threads/2/resume", `{"decisions":[{"type":"approve","tool_call_id":"w1"}]}`)
		if response.StatusCode != 200 {
			raw, _ := io.ReadAll(response.Body)
			t.Fatalf("the resume was refused: %d %s", response.StatusCode, raw)
		}
		resumed, _ := readFrames(t, response.Body)
		response.Body.Close()
		if after := readResumed(t, page.parts, resumed); after.failure != nil {
			t.Fatalf("the resume failed: %v", after.failure)
		}
		var contents []string
		rows, _ := pool.Query(ctx, `SELECT role::text || ' ' || coalesce(author_id::text, '-') || ' ' || content::text FROM new_chat_messages WHERE thread_id = 2 ORDER BY id`)
		for rows.Next() {
			var line string
			_ = rows.Scan(&line)
			contents = append(contents, line)
		}
		if len(contents) != 3 || !strings.HasPrefix(contents[0], "USER "+ada) || !strings.Contains(contents[1], `"toolCallId": "w1"`) || !strings.Contains(contents[1], `"result": {"status": "completed"`) || !strings.Contains(contents[2], `Noted [citation:482].`) {
			t.Fatalf("rows:\n%s", strings.Join(contents, "\n"))
		}
		var status string
		var state []byte
		_ = pool.QueryRow(ctx, `SELECT status, state FROM soloclef_agent_checkpoints WHERE thread_id = 'chat-2'`).Scan(&status, &state)
		var saved agent.State
		if err := json.Unmarshal(state, &saved); err != nil || status != "done" || len(saved.Messages) != 4 || len(saved.Citations) != 1 {
			t.Errorf("checkpoint: %s, %d messages, %v", status, len(saved.Messages), err)
		}
	})
}

// trustFrom makes the scripted assembly read trust from the real table.
func trustFrom(store Store) appOption {
	return func(_ *Service, sc *scripted) { sc.trusted = store.Trusted }
}

func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
