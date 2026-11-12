package deliverables

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/agent"
	"git.danvics.com/danvics/soloclef/soloclef_agent/checkpoint"
	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

func cards(n int) []Card {
	out := make([]Card, n)
	for i := range out {
		out[i] = Card{Front: "Question " + strconv.Itoa(i+1) + "?", Back: "Answer " + strconv.Itoa(i+1) + "."}
	}
	return out
}

func TestADeckIsCheckedAgainstTheWholeSchemaAtOnce(t *testing.T) {
	good := cards(5)
	good[0].Hint = " think "
	deck, err := NewDeck("  Cell biology ", good)
	if err != nil {
		t.Fatal(err)
	}
	if deck.Title != "Cell biology" || deck.SchemaVersion != 1 || deck.Cards[0].Hint != "think" {
		t.Fatalf("deck = %+v", deck)
	}

	for name, c := range map[string]struct {
		title string
		cards []Card
		says  string
	}{
		"too few cards":       {"T", cards(4), "5 to 100 cards; this one has 4"},
		"too many cards":      {"T", cards(101), "this one has 101"},
		"no title":            {"  ", cards(5), "the title is empty"},
		"a two-line title":    {"a\nb", cards(5), "one line"},
		"a title too long":    {strings.Repeat("t", 201), cards(5), "longer than 200"},
		"an empty back":       {"T", append(cards(4), Card{Front: "Q?"}), "card 5: back_text is empty"},
		"a front too long":    {"T", append(cards(4), Card{Front: strings.Repeat("f", 4001), Back: "b"}), "front_text is longer than 4000"},
		"a control character": {"T", append(cards(4), Card{Front: "Q\x07?", Back: "b"}), "control character (U+0007)"},
		"the same front twice, in another case and width": {"T", append(cards(4), Card{Front: "QUESTION １?", Back: "b"}), "card 5 has the same front as card 1"},
		"the same front twice, spaced differently":        {"T", append(cards(4), Card{Front: "Question \t 1?", Back: "b"}), "card 5 has the same front as card 1"},
		// The viewer refuses a file with one bad delimiter anywhere in it,
		// so a deck that saved with one would never open.
		"mathematics that is never closed": {"T", append(cards(4), Card{Front: "Q?", Back: `\(x^2`}), `card 5: back_text has a LaTeX delimiter \( that is never closed`},
	} {
		_, err := NewDeck(c.title, c.cards)
		if err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", name, err)
		}
	}

	// Every problem is reported in one answer, so a model fixes them in one
	// round; a flood of them is cut short.
	_, err = NewDeck("", []Card{{}, {}})
	for _, says := range []string{"the title is empty", "this one has 2", "card 1: front_text is empty", "card 2: back_text is empty"} {
		if !strings.Contains(err.Error(), says) {
			t.Errorf("missing %q in %v", says, err)
		}
	}
	bad := make([]Card, 30)
	if _, err := NewDeck("T", bad); !strings.Contains(err.Error(), "more problems") {
		t.Errorf("sixty problems were all listed: %v", err)
	}
}

// The file is what internal/artifacts and the web viewer read: this exact
// shape, a missing hint left out and never null.
func TestTheDeckFileAndItsMarkdown(t *testing.T) {
	deck, err := NewDeck("Sets", []Card{
		{Front: "Is a < b?", Back: `Yes: \(a < b\)`, Hint: "order"},
		{Front: "Q2", Back: "A2"}, {Front: "Q3", Back: "A3"}, {Front: "Q4", Back: "A4"}, {Front: "Q5", Back: "A5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := deck.File()
	if err != nil {
		t.Fatal(err)
	}
	want := `{"schema_version":1,"title":"Sets","cards":[{"front_text":"Is a < b?","back_text":"Yes: \\(a < b\\)","hint_text":"order"},{"front_text":"Q2","back_text":"A2"},` +
		`{"front_text":"Q3","back_text":"A3"},{"front_text":"Q4","back_text":"A4"},{"front_text":"Q5","back_text":"A5"}]}`
	if string(file) != want {
		t.Fatalf("file\n got %s\nwant %s", file, want)
	}
	markdown := deck.Markdown()
	if !strings.HasPrefix(markdown, "# Sets\n\n## Card 1\n\n### Front\n\nIs a < b?\n\n### Back\n\nYes: \\(a < b\\)\n\n### Hint\n\norder\n\n## Card 2\n") || strings.Count(markdown, "### Hint") != 1 {
		t.Fatalf("markdown:\n%s", markdown)
	}
}

func TestNames(t *testing.T) {
	for title, want := range map[string]string{
		"Cell Biology: Mitosis & Meiosis!": "cell-biology-mitosis-meiosis",
		"Ελληνικά 101":                     "ελληνικά-101",
		"???":                              "flashcards",
	} {
		if got := slug(title, FormatFlashcards); got != want {
			t.Errorf("slug(%q) = %q, want %q", title, got, want)
		}
	}
	if got := slug("???", FormatQuiz); got != "quiz" {
		t.Errorf("a title with nothing to name a file after: %q", got)
	}
	// The document's name and hashes moved with the saver
	// (soloclef_studio/artifactsave, names_test.go).
}

// ledger is the artifact tables in memory. It keeps the unit Create must be:
// it stores the file through the real file store, and a failure after that
// removes the file again.
type ledger struct {
	created  []NewArtifact
	keys     []string
	failRows error
	indexed  []int64
	indexErr error
	// studio is where the specialist's podcasts are opened.
	studio studio
	// painter, proofreader and desk are the image model, the model that
	// reads an image back, and the video queue (media_test.go).
	painter     painter
	proofreader proofreader
	desk        desk
	// shed is the sandbox the built documents are made in
	// (documents_test.go).
	shed shed
}

// studio is a PodcastOpener that remembers what it was asked for.
type studio struct {
	opened []PodcastRequest
	owners []Owner
	fail   error
}

func (s *studio) Check() error { return nil }

func (s *studio) Open(_ context.Context, owner Owner, request PodcastRequest) (int64, error) {
	if s.fail != nil {
		return 0, s.fail
	}
	s.opened = append(s.opened, request)
	s.owners = append(s.owners, owner)
	return int64(900 + len(s.opened)), nil
}

func (l *ledger) Create(ctx context.Context, artifact NewArtifact, files filestore.Writer, key func(int64) string) (Created, error) {
	id := int64(40 + len(l.created) + 1)
	if !artifact.HasFile() {
		// The contract of an artifact with no file: nothing is stored and
		// key is never asked.
		if l.failRows != nil {
			return Created{}, l.failRows
		}
		l.created = append(l.created, artifact)
		l.keys = append(l.keys, "")
		return Created{ArtifactID: id, DocumentID: id + 100}, nil
	}
	k := key(id)
	if err := files.Put(ctx, k, artifact.Data, artifact.MimeType); err != nil {
		return Created{}, err
	}
	if l.failRows != nil {
		_ = files.Remove(ctx, k)
		return Created{}, l.failRows
	}
	l.created = append(l.created, artifact)
	l.keys = append(l.keys, k)
	made := Created{ArtifactID: id, DocumentID: id + 100, FileID: id + 200}
	if len(artifact.Preview) > 0 {
		made.PreviewFileID = id + 300
	}
	return made, nil
}

func specialist(t testing.TB, m model.Model, l *ledger) (Specialist, *filestore.Local) {
	t.Helper()
	local, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := Specialist{
		Model: m, Store: checkpoint.NewMemory(), Limits: Limits{MaxSteps: 8, Timeout: time.Minute},
		Podcasts: &l.studio,
		Images:   &l.painter, Reader: &l.proofreader, Videos: &l.desk,
		Workshop: &l.shed, Verified: NewVerifications(),
		Saver: Saver{
			Rows: l, Files: local, NewID: func() string { return "0123456789abcdef0123456789abcdef" },
			Index: func(_ context.Context, documentID int64, _ string) error {
				l.indexed = append(l.indexed, documentID)
				return l.indexErr
			},
			Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		},
	}
	if err := s.Check(); err != nil {
		t.Fatal(err)
	}
	return s, local
}

func deckArgs(t testing.TB, title string, n int) string {
	t.Helper()
	args, err := json.Marshal(map[string]any{"format": "flashcards", "title": title, "cards": cards(n)})
	if err != nil {
		t.Fatal(err)
	}
	return string(args)
}

// The specialist as the main agent runs it: handed a task, it loads the
// instructions, saves a deck, and answers. The saved file is the stored one.
func TestTheSpecialistMakesFlashcardsEndToEnd(t *testing.T) {
	l := &ledger{}
	nestedModel := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "i1", Name: InstructionsTool, Args: `{"artifact_type":"Flashcards"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "s1", Name: SaveTool, Args: deckArgs(t, "Mitosis", 6)}}},
		modeltest.Reply{Text: "Saved a deck of 6 flashcards titled Mitosis."},
	)
	s, files := specialist(t, nestedModel, l)
	owner := Owner{ThreadID: 1, UserID: "u-1"}
	delegate, err := s.Delegate(owner)
	if err != nil {
		t.Fatal(err)
	}
	mainModel := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "d1", Name: DelegateName, Args: `{"task":"Make 6 flashcards on mitosis from: ..."}`}}},
		modeltest.Reply{Text: "Your flashcards are ready."},
	)
	main, err := agent.New(agent.Config{Model: mainModel, MaxSteps: 6, MaxParallelTools: 1, Store: s.Store, Delegates: []agent.Delegate{delegate}})
	if err != nil {
		t.Fatal(err)
	}
	var saved string
	result, err := main.Start(context.Background(), "chat-1", []model.Message{model.User("flashcards please")}, func(ev event.Event) {
		if ev.Kind == event.ToolResult && ev.Tool == SaveTool {
			saved = ev.Text
			if ev.Parent != "d1" || ev.CallID != "d1/s1" || ev.IsError {
				t.Errorf("the save's result event = %+v", ev)
			}
		}
	})
	if err != nil || result.Answer != "Your flashcards are ready." {
		t.Fatalf("result %+v, err %v", result, err)
	}
	// What the model and the page's card are told.
	want := `{"status":"saved","artifact_id":41,"generation":1,"title":"Mitosis","format":"flashcards","files":[{"file_id":241,"role":"primary","filename":"mitosis.json","mime_type":"application/json","size_bytes":` + strconv.Itoa(len(l.created[0].Data)) + `}]}`
	if saved != want {
		t.Fatalf("save result\n got %s\nwant %s", saved, want)
	}
	// The rows asked for: this turn's owner, whatever the model wrote.
	made := l.created[0]
	if made.Owner != owner || made.Format != "flashcards" || made.ToolCallID == "" || made.MimeType != "application/json" || !strings.HasPrefix(made.Markdown, "# Mitosis\n") {
		t.Errorf("artifact = %+v", made)
	}
	if string(made.Metadata) != `{"verification": {"verified": true, "reason": null}}` {
		t.Errorf("metadata = %s", made.Metadata)
	}
	// The stored file, under the key every reader of the store expects,
	// is a deck this package's own reader accepts.
	if l.keys[0] != "artifacts/u-1/41/primary/0123456789abcdef0123456789abcdef.json" {
		t.Errorf("key = %s", l.keys[0])
	}
	body, err := files.Open(context.Background(), l.keys[0])
	if err != nil {
		t.Fatal(err)
	}
	stored, _ := io.ReadAll(body)
	body.Close()
	var deck Deck
	if err := json.Unmarshal(stored, &deck); err != nil || deck.SchemaVersion != 1 || len(deck.Cards) != 6 || deck.Title != "Mitosis" {
		t.Fatalf("stored deck = %s (%v)", stored, err)
	}
	if len(l.indexed) != 1 || l.indexed[0] != 141 {
		t.Errorf("indexing was asked for %v", l.indexed)
	}
	// The main agent's model saw only the specialist's answer.
	shown := mainModel.Requests()[1].Messages
	if last := shown[len(shown)-1]; last.Content != "Saved a deck of 6 flashcards titled Mitosis." {
		t.Errorf("the main agent was told %q", last.Content)
	}
}

// A deck with problems is not saved; the model is told all of them and
// saves a corrected one.
func TestABadDeckIsRefusedAndACorrectedOneSaved(t *testing.T) {
	l := &ledger{}
	script := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "s1", Name: SaveTool, Args: deckArgs(t, "Short", 2)}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "s2", Name: SaveTool, Args: `{"format":"flashcards","title":"T","cards":[],"tags":["x"]}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "s3", Name: SaveTool, Args: deckArgs(t, "Enough", 5)}}},
		modeltest.Reply{Text: "Saved."},
	)
	s, _ := specialist(t, script, l)
	delegate, _ := s.Delegate(Owner{ThreadID: 1, UserID: "u"})
	if _, err := delegate.Agent.Start(context.Background(), "t", []model.Message{model.User("task")}, event.Discard); err != nil {
		t.Fatal(err)
	}
	if len(l.created) != 1 || l.created[0].Title != "Enough" {
		t.Fatalf("created = %+v", l.created)
	}
	requests := script.Requests()
	if told := requests[1].Messages[len(requests[1].Messages)-1].Content; !strings.Contains(told, "5 to 100 cards; this one has 2") {
		t.Errorf("after a short deck the model was told %q", told)
	}
	if told := requests[2].Messages[len(requests[2].Messages)-1].Content; !strings.Contains(told, "tags") {
		t.Errorf("after an invented field the model was told %q", told)
	}
}

// The kinds that are not built are refused by name, before anything is made.
func TestTheOtherKindsAreRefusedByName(t *testing.T) {
	l := &ledger{}
	s, _ := specialist(t, modeltest.NewScript(), l)
	instructions := s.instructionsTool(Owner{UserID: "u-1"})
	for kind, says := range map[string]string{
		"markdown": "Plain documents cannot be made",
		"poem":     "is not a kind of artifact. The kinds that can be made are " + kindNames() + ".",
	} {
		_, err := instructions.Run(context.Background(), toolCall(`{"artifact_type":"`+kind+`"}`))
		if err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v", kind, err)
		}
	}
	save := s.saveTool(Owner{UserID: "u-1"})
	for args, says := range map[string]string{
		`{"format":"markdown","title":"T"}`:                                              "Plain documents cannot be made",
		`{"format":"podcast","title":"T"}`:                                               "a podcast is not saved with save_artifact; call generate_podcast",
		`{"format":"image","title":"T"}`:                                                 "an image is not saved with save_artifact; call generate_image",
		`{"format":"Video","title":"T"}`:                                                 "a video is not saved with save_artifact; call enqueue_deliverable_job",
		`{"format":"infographic","title":"T"}`:                                           "the infographic was not made: markdown is missing",
		`{"format":"infographic","title":"T","cards":[],"markdown":"# x"}`:               `format "infographic" is saved from markdown alone; remove cards`,
		`{"format":"sonnet","title":"T"}`:                                                `format "sonnet" cannot be saved`,
		`{"format":"quiz","title":"T","cards":[]}`:                                       `format "quiz" is saved from questions alone; remove cards`,
		`{"format":"flashcards","title":"T","cards":[],"markdown":"# x","questions":[]}`: `remove questions and markdown`,
		`{"format":"mindmap","title":"T"}`:                                               "markdown is missing",
	} {
		if _, err := save.Run(context.Background(), toolCall(args)); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v", args, err)
		}
	}
	if len(l.created) != 0 {
		t.Fatal("something was saved")
	}
}

// A storage failure is a sentence to the model and leaves no file behind; a
// failure to queue the indexing does not fail a save that succeeded.
func TestFailuresWhileSaving(t *testing.T) {
	l := &ledger{failRows: errors.New(`relation "artifacts" at 10.0.0.5`)}
	s, files := specialist(t, modeltest.NewScript(), l)
	save := s.saveTool(Owner{ThreadID: 1, UserID: "u"})
	_, err := save.Run(context.Background(), toolCall(deckArgs(t, "Deck", 5)))
	if err == nil || strings.Contains(err.Error(), "10.0.0.5") || !strings.Contains(err.Error(), "was not saved") {
		t.Fatalf("err = %v", err)
	}
	if _, statErr := files.Stat(context.Background(), "artifacts/u/41/primary/0123456789abcdef0123456789abcdef.json"); !errors.Is(statErr, filestore.ErrNotFound) {
		t.Fatalf("a file was left behind by a failed save: %v", statErr)
	}

	l.failRows, l.indexErr = nil, errors.New("queue down")
	out, err := save.Run(context.Background(), toolCall(deckArgs(t, "Deck", 5)))
	if err != nil || !strings.Contains(out, `"status":"saved"`) {
		t.Fatalf("a save whose indexing could not be queued: %q, %v", out, err)
	}
}

func TestTheSpecialistRefusesToStartUnconfigured(t *testing.T) {
	if err := (Specialist{}).Check(); err == nil {
		t.Fatal("an empty specialist was accepted")
	}
	s, _ := specialist(t, modeltest.NewScript(), &ledger{})
	s.Limits.Timeout = 0
	if err := s.Check(); err == nil {
		t.Fatal("a specialist with no time limit was accepted")
	}
	s.Limits.Timeout = time.Second
	s.Podcasts = nil
	if err := s.Check(); err == nil || !strings.Contains(err.Error(), "Podcasts is not set") {
		t.Fatalf("a specialist that cannot open a podcast: %v", err)
	}
	s.Podcasts = Podcasts{}
	if err := s.Check(); err == nil || !strings.Contains(err.Error(), "Podcasts.Rows is not set") {
		t.Fatalf("a podcast opener with no database: %v", err)
	}
	s.Podcasts = &studio{}
	for missing, unset := range map[string]func(*Specialist){
		"Images is not set": func(s *Specialist) { s.Images = nil },
		"Reader is not set": func(s *Specialist) { s.Reader = nil },
		"Videos is not set": func(s *Specialist) { s.Videos = nil },
		// A real client or queue that is half set up is refused as well.
		"ProxyImages.BaseURL is not set":     func(s *Specialist) { s.Images = ProxyImages{} },
		"VisionImageReader.Sight is not set": func(s *Specialist) { s.Reader = VisionImageReader{} },
		"Videos.Rows is not set":             func(s *Specialist) { s.Videos = Videos{} },
	} {
		broken := s
		unset(&broken)
		if err := broken.Check(); err == nil || !strings.Contains(err.Error(), missing) {
			t.Fatalf("%s: %v", missing, err)
		}
	}
	s.Saver.Index = nil
	if err := s.Check(); err == nil {
		t.Fatal("a saver that cannot ask for indexing was accepted")
	}
}

func BenchmarkNewDeck(b *testing.B) {
	deck := cards(50)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := NewDeck("Fifty cards", deck); err != nil {
			b.Fatal(err)
		}
	}
}
