package chat

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
)

// A notebook chat's question that mentions documents: the mentions are
// handed to the notebook before the agent is built, for the account the
// turn runs for, and the two namings the notebook is owed are queued as the
// worker tier's task reads them.
func TestANotebookTurnLinksItsMentionsAndQueuesTheNaming(t *testing.T) {
	const body = `{"chat_id":1,"user_query":"What does @Genetics say about alleles?","mentioned_document_ids":[14],"mentioned_folder_ids":[5]}`
	a := newApp(t, modeltest.NewScript(modeltest.Reply{Text: "It says."}), nil, func(_ *Service, sc *scripted) {
		sc.rows.notebook = NotebookTurn{NotebookID: 30, AddedDocuments: []int64{14}, NameFromSources: true, NameFromQuestion: true}
	})
	if frames, done := a.ask(ada, body); !done || readAll(t, frames).failure != nil {
		t.Fatal("the turn did not finish")
	}
	asked := a.rows.prepared
	if len(asked) != 1 || asked[0].turn.UserID != ada || asked[0].turn.ThreadID != 1 || len(asked[0].turn.DocumentIDs) != 1 || asked[0].turn.DocumentIDs[0] != 14 || len(asked[0].turn.FolderIDs) != 1 {
		t.Fatalf("the notebook was prepared with %+v", asked)
	}
	// notebook_autoname(notebook_id, trigger, question), apply_async: from
	// the sources after a moment, and from the question now.
	queued, _ := json.Marshal(a.rows.queued)
	var messages []struct {
		Task        string
		Args        []any
		ArgsAsTuple bool
		Countdown   time.Duration
		Queue       string
	}
	if err := json.Unmarshal(queued, &messages); err != nil || len(messages) != 2 {
		t.Fatalf("queued %s", queued)
	}
	source, question := messages[0], messages[1]
	if source.Task != "notebook_autoname" || source.ArgsAsTuple || source.Queue != "" || source.Countdown != 4*time.Second || len(source.Args) != 3 || source.Args[0] != float64(30) || source.Args[1] != "source" || source.Args[2] != nil {
		t.Errorf("the naming from sources = %+v", source)
	}
	if question.Task != "notebook_autoname" || question.Countdown != 0 || question.Args[1] != "question" || question.Args[2] != "What does @Genetics say about alleles?" {
		t.Errorf("the naming from the question = %+v", question)
	}
}

func TestAPlainChatQueuesNothingAndABrokerThatIsDownCostsNoAnswer(t *testing.T) {
	const mention = `{"chat_id":1,"user_query":"About @Genetics","mentioned_document_ids":[14]}`
	// A notebook that is owed no naming queues none, mention or not.
	a := newApp(t, modeltest.NewScript(modeltest.Reply{Text: "ok"}), nil, func(_ *Service, sc *scripted) {
		sc.rows.notebook = NotebookTurn{NotebookID: 30}
	})
	a.ask(ada, mention)
	if asked := a.rows.prepared; len(asked) != 1 || asked[0].turn.UserID != ada {
		t.Errorf("the notebook was prepared with %+v", asked)
	}
	if len(a.rows.queued) != 0 {
		t.Errorf("queued %+v for a notebook that is owed no naming", a.rows.queued)
	}
	// Another account's mention never reaches the notebook at all: the
	// thread is not theirs, so the turn is refused before anything is
	// prepared.
	if got := a.call(ben, "POST", TurnPath, mention); got.status != 404 || len(a.rows.prepared) != 1 {
		t.Errorf("Ben mentioning into Ada's notebook chat: %d, prepared %d times", got.status, len(a.rows.prepared))
	}

	// A thread that is no notebook's queues nothing, mention or not.
	plain := newApp(t, modeltest.NewScript(modeltest.Reply{Text: "ok"}), nil)
	plain.ask(ada, mention)
	if len(plain.rows.queued) != 0 {
		t.Errorf("a plain chat queued %+v", plain.rows.queued)
	}

	// A broker that is down costs the notebook its new name, not the
	// person their answer.
	down := newApp(t, modeltest.NewScript(modeltest.Reply{Text: "Still answered."}), nil, func(_ *Service, sc *scripted) {
		sc.rows.notebook = NotebookTurn{NotebookID: 30, NameFromQuestion: true}
		sc.rows.failQueue = true
	})
	frames, done := down.ask(ada, mention)
	if page := readAll(t, frames); !done || page.failure != nil {
		t.Fatalf("the turn failed with the broker: %v", page.failure)
	}

	// A question of nothing but space names nothing.
	if hasText(" \n\t") || !hasText(" a ") {
		t.Error("hasText")
	}
}

func TestHowOftenANotebookHasNamedItself(t *testing.T) {
	for column, want := range map[string]int{
		``: 0, `null`: 0, `{}`: 0, `{"runs":0}`: 0, `{"runs":1,"at":"2026-01-01T00:00:00+00:00"}`: 1,
		`{"runs":2}`: 2, `{"runs":"2"}`: 0, `{"runs":-1}`: 0, `{"runs":1.5}`: 0, `[1]`: 0, `"x"`: 0,
	} {
		if got := autoTitleRuns([]byte(column)); got != want {
			t.Errorf("%s: %d, want %d", column, got, want)
		}
	}
}

// The statements, over the real tables: what becomes a source, what does
// not, and what the turn then reads.
func TestAMentionBecomesASourceOverARealDatabase(t *testing.T) {
	address := os.Getenv(databaseVariable)
	if address == "" {
		t.Skipf("%s is not set; this test needs a throwaway Postgres", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, seed+`
INSERT INTO documents (id, title, document_type, content, content_hash, user_id, status) VALUES
  (15, 'Ecology', 'FILE', 'x', 'h15', '`+ada+`', '{"state": "ready"}'),
  (16, 'Still uploading', 'FILE', 'x', 'h16', '`+ada+`', '{"state": "processing"}');
INSERT INTO folders (id, name, position, user_id) VALUES (6, 'Physics', 'a1', '`+ada+`');
UPDATE notebooks SET updated_at = '2020-01-01', title_source = 'auto' WHERE id = 31;
UPDATE notebooks SET updated_at = '2020-01-01', title_source = 'auto', auto_title = '{"runs": 1, "at": "2026-01-01T00:00:00+00:00", "sources": 1, "basis": "sources"}' WHERE id = 30;
`); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	store := Store{Pool: pool}
	sources := func(notebook int64) string {
		t.Helper()
		rows, err := pool.Query(ctx, `SELECT COALESCE('d' || document_id, 'f' || folder_id) || CASE WHEN enabled THEN '' ELSE '-off' END FROM notebook_sources WHERE notebook_id = $1 ORDER BY id`, notebook)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var one string
			if err := rows.Scan(&one); err != nil {
				t.Fatal(err)
			}
			out = append(out, one)
		}
		return strings.Join(out, " ")
	}
	touched := func(notebook int64) bool {
		var recent bool
		if err := pool.QueryRow(ctx, `SELECT updated_at > now() - interval '1 minute' FROM notebooks WHERE id = $1`, notebook).Scan(&recent); err != nil {
			t.Fatal(err)
		}
		return recent
	}

	// Thread 3 is notebook 30's, which has named itself once. Mentioned:
	// 15 (new, ready), 16 (new, not ready), 12 (already a source), 13
	// (a source its owner unticked), 90 (another account's), 404 (no such
	// document); folder 6 (new), folder 5 (already a source) and folder 8
	// (another account's).
	turn := Turn{ThreadID: 3, UserID: ada, DocumentIDs: []int64{16, 15, 12, 13, 90, 404, 15}, FolderIDs: []int64{6, 5, 8, 999}}
	done, err := store.PrepareNotebook(ctx, turn)
	if err != nil {
		t.Fatal(err)
	}
	if done.NotebookID != 30 || len(done.AddedDocuments) != 2 || done.AddedDocuments[0] != 15 || done.AddedDocuments[1] != 16 || len(done.AddedFolders) != 1 || done.AddedFolders[0] != 6 {
		t.Fatalf("added %+v", done)
	}
	// It gained a readable source, so it is named again from its sources;
	// it has been named before, so not from the question.
	if !done.NameFromSources || done.NameFromQuestion {
		t.Errorf("the naming owed = %+v", done)
	}
	const after = "d12 d13-off f5 d15 d16 f6"
	if got := sources(30); got != after {
		t.Errorf("notebook 30's sources = %s, want %s: nothing of another account, and the unticked one still unticked", got, after)
	}
	if !touched(30) {
		t.Error("the notebook did not rise in the list")
	}
	// The turn's search now reaches the mentioned documents, and still
	// nothing unticked and nothing of another account.
	about, err := store.Context(ctx, turn)
	if err != nil {
		t.Fatal(err)
	}
	if got := about.Notebook.DocumentIDs; !containsID(got, 15) || containsID(got, 13) || containsID(got, 90) {
		t.Errorf("the turn may read %v", got)
	}

	// The same mentions again add nothing and move nothing.
	if _, err := pool.Exec(ctx, `UPDATE notebooks SET updated_at = '2020-01-01' WHERE id = 30`); err != nil {
		t.Fatal(err)
	}
	if again, err := store.PrepareNotebook(ctx, turn); err != nil || len(again.AddedDocuments)+len(again.AddedFolders) != 0 || again.NameFromSources || touched(30) {
		t.Errorf("a second time: %+v, %v, touched %v", again, err, touched(30))
	}

	// Thread 4 is notebook 31's, which names itself and never has. A
	// document that is not ready yet becomes a source and does not name
	// the notebook from sources; the question still names it.
	empty := Turn{ThreadID: 4, UserID: ada, DocumentIDs: []int64{16}}
	if unready, err := store.PrepareNotebook(ctx, empty); err != nil || len(unready.AddedDocuments) != 1 || unready.NameFromSources || !unready.NameFromQuestion {
		t.Errorf("a document still uploading: %+v, %v", unready, err)
	}

	// The boundary between two accounts, asked of the statements directly
	// (the routes would have refused each of these at the gate):
	//   - a thread that is no notebook's: nothing;
	//   - Dee naming Ada's notebook chat, with a document of her own and
	//     with one of Ada's: no notebook, nothing linked;
	//   - Dee's own notebook chat mentioning Ada's documents and folder:
	//     her notebook gains nothing of Ada's;
	//   - Ada naming Dee's notebook chat: no notebook.
	deesBefore := sources(80)
	for name, other := range map[string]Turn{
		"a plain chat":                   {ThreadID: 1, UserID: ada, DocumentIDs: []int64{15}},
		"Dee on Ada's notebook chat":     {ThreadID: 3, UserID: dee, DocumentIDs: []int64{90, 14}, FolderIDs: []int64{8, 5}},
		"Ada on Dee's notebook chat":     {ThreadID: 10, UserID: ada, DocumentIDs: []int64{14, 90}, FolderIDs: []int64{5}},
		"Ben, who has no notebook chats": {ThreadID: 3, UserID: ben, DocumentIDs: []int64{14}},
	} {
		if nothing, err := store.PrepareNotebook(ctx, other); err != nil || nothing.NotebookID != 0 || len(nothing.AddedDocuments)+len(nothing.AddedFolders) != 0 {
			t.Errorf("%s: %+v, %v", name, nothing, err)
		}
	}
	crossed, err := store.PrepareNotebook(ctx, Turn{ThreadID: 10, UserID: dee, DocumentIDs: []int64{12, 14, 15}, FolderIDs: []int64{5, 6}})
	if err != nil || crossed.NotebookID != 80 || len(crossed.AddedDocuments)+len(crossed.AddedFolders) != 0 {
		t.Errorf("Dee mentioning Ada's documents in her own notebook chat: %+v, %v", crossed, err)
	}
	if got := sources(30); got != after {
		t.Errorf("notebook 30's sources afterwards = %s", got)
	}
	if got := sources(80); got != deesBefore {
		t.Errorf("Dee's notebook's sources = %s, were %s", got, deesBefore)
	}
	// And what each notebook chat may read is unchanged by all of it.
	if deesAbout, err := store.Context(ctx, Turn{ThreadID: 10, UserID: dee}); err != nil || len(deesAbout.Notebook.DocumentIDs) != 1 || deesAbout.Notebook.DocumentIDs[0] != 90 {
		t.Errorf("Dee's notebook chat may read %+v, %v", deesAbout.Notebook, err)
	}
}

func containsID(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// BenchmarkPrepareNotebook is what every turn pays before its agent is
// built: one read for a plain chat, and the linking statements for a
// notebook chat whose mentions are already sources (the common case after
// the first mention).
func BenchmarkPrepareNotebook(b *testing.B) {
	address := os.Getenv(databaseVariable)
	if address == "" {
		b.Skipf("%s is not set", databaseVariable)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, address)
	if err != nil {
		b.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, seed); err != nil {
		b.Fatal(err)
	}
	store := Store{Pool: pool}
	for name, turn := range map[string]Turn{
		"plain chat":             {ThreadID: 1, UserID: ada},
		"notebook, two mentions": {ThreadID: 3, UserID: ada, DocumentIDs: []int64{12, 14}},
	} {
		b.Run(name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if _, err := store.PrepareNotebook(ctx, turn); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
