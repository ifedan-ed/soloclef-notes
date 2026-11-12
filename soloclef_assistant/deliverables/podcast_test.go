package deliverables

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/event"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/voices"
)

// The specialist handed a podcast: it loads the podcast's instructions and
// opens one for the turn's owner. Nothing is saved as an artifact.
func TestTheSpecialistOpensAPodcastEndToEnd(t *testing.T) {
	l := &ledger{}
	script := modeltest.NewScript(
		modeltest.Reply{Calls: []model.ToolCall{{ID: "i1", Name: InstructionsTool, Args: `{"artifact_type":"podcast"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "p1", Name: PodcastTool, Args: `{"podcast_title":"  ","source_content":"","user_prompt":"x"}`}}},
		modeltest.Reply{Calls: []model.ToolCall{{ID: "p2", Name: PodcastTool, Args: `{"podcast_title":" Mitosis explained ","source_content":"Mitosis has four phases.\r\nProphase comes first.\u000c","user_prompt":"Keep it light."}`}}},
		modeltest.Reply{Text: "The podcast Mitosis explained is set up and waiting for the brief to be confirmed."},
	)
	s, _ := specialist(t, script, l)
	owner := Owner{ThreadID: 3, UserID: "u-1"}
	delegate, err := s.Delegate(owner)
	if err != nil {
		t.Fatal(err)
	}
	var opened event.Event
	if _, err := delegate.Agent.Start(context.Background(), "t", []model.Message{model.User("Make a podcast from: ...")}, func(ev event.Event) {
		if ev.Kind == event.ToolResult && ev.Tool == PodcastTool && !ev.IsError {
			opened = ev
		}
	}); err != nil {
		t.Fatal(err)
	}
	requests := script.Requests()
	if told := requests[1].Messages[len(requests[1].Messages)-1].Content; !strings.Contains(told, "Call generate_podcast once") || !strings.Contains(told, "Do not call save_artifact") {
		t.Errorf("the podcast instructions were %q", told)
	}
	if told := requests[2].Messages[len(requests[2].Messages)-1].Content; !strings.Contains(told, "the podcast was not created: podcast_title is empty; source_content is empty") {
		t.Errorf("after an empty podcast the model was told %q", told)
	}
	want := `{"status":"awaiting_brief","podcast_id":901,"title":"Mitosis explained","message":"` + podcastOpenedMessage + `"}`
	if opened.Text != want {
		t.Fatalf("result\n got %s\nwant %s", opened.Text, want)
	}
	// The owner is the turn's; the source is stored with one kind of line
	// ending and without the form feed.
	if len(l.studio.opened) != 1 || l.studio.owners[0] != owner {
		t.Fatalf("opened %+v for %+v", l.studio.opened, l.studio.owners)
	}
	if got := l.studio.opened[0]; got != (PodcastRequest{Title: "Mitosis explained", Source: "Mitosis has four phases.\nProphase comes first.", Focus: "Keep it light."}) {
		t.Errorf("request = %+v", got)
	}
	if len(l.created) != 0 {
		t.Error("an artifact was saved for a podcast")
	}
	// The card gets the three fields it draws and not the model's message.
	card, ok := ToolCard(opened)
	if !ok || card != (PodcastCard{Status: "awaiting_brief", PodcastID: 901, Title: "Mitosis explained"}) {
		t.Errorf("card = %+v, %v", card, ok)
	}
}

func TestAPodcastThatCannotBeOpened(t *testing.T) {
	l := &ledger{}
	s, _ := specialist(t, modeltest.NewScript(), l)
	open := s.podcastTool(Owner{ThreadID: 1, UserID: "u"})
	good := `{"podcast_title":"T","source_content":"Material."}`

	// A reason the person can be told is passed on in its own words.
	l.studio.fail = &PodcastRefusal{Reason: noSpeechService}
	if _, err := open.Run(context.Background(), toolCall(good)); err == nil || !strings.HasPrefix(err.Error(), noSpeechService+" The podcast was not created") {
		t.Errorf("a refusal: %v", err)
	}
	// Anything else is a sentence, and its cause stays in the log.
	l.studio.fail = errors.New(`relation "podcast_runs" at 10.0.0.5`)
	if _, err := open.Run(context.Background(), toolCall(good)); err == nil || strings.Contains(err.Error(), "10.0.0.5") || !strings.Contains(err.Error(), "it was not created") {
		t.Errorf("a storage failure: %v", err)
	}
	l.studio.fail = nil
	for name, c := range map[string]struct{ args, says string }{
		"a title too long":   {`{"podcast_title":"` + strings.Repeat("t", 501) + `","source_content":"x"}`, "podcast_title is longer than 500 characters"},
		"a two-line title":   {`{"podcast_title":"a\nb","source_content":"x"}`, "podcast_title must be one line"},
		"a steer too long":   {`{"podcast_title":"T","source_content":"x","user_prompt":"` + strings.Repeat("f", 2001) + `"}`, "user_prompt is longer than 2000 characters"},
		"only control bytes": {`{"podcast_title":"T","source_content":"\u0000\u0007 "}`, "source_content is empty"},
		"an invented field":  {`{"podcast_title":"T","source_content":"x","voice":"af_heart"}`, "voice"},
	} {
		if _, err := open.Run(context.Background(), toolCall(c.args)); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if len(l.studio.opened) != 0 {
		t.Fatalf("a podcast was opened: %+v", l.studio.opened)
	}
}

// newPodcast is one podcast the rows were asked to create.
type newPodcast struct {
	Title, SourceContent string
	Spec                 []byte
	ThreadID             int64
}

// podcastRows is the podcast table in memory.
type podcastRows struct {
	lastBrief  []byte
	created    []newPodcast
	createdFor []string
	failCreate error
	asked      []string
}

func (r *podcastRows) Create(_ context.Context, owner Owner, title, source string, propose func([]byte) ([]byte, error)) (int64, error) {
	r.asked = append(r.asked, owner.UserID)
	spec, err := propose(r.lastBrief)
	if err != nil {
		// A transaction that fails leaves no row.
		return 0, err
	}
	if r.failCreate != nil {
		return 0, r.failCreate
	}
	r.created = append(r.created, newPodcast{Title: title, SourceContent: source, Spec: spec, ThreadID: owner.ThreadID})
	r.createdFor = append(r.createdFor, owner.UserID)
	return int64(60 + len(r.created)), nil
}

// kokoro is the catalogue of a deployment whose speech service is Kokoro,
// which needs no proxy to list its voices.
func kokoro(t testing.TB, hosts ...string) VoiceCatalogue {
	t.Helper()
	catalogue, err := voices.New(voices.Settings{
		Service: "openai/hosted-kokoro-82m", APIBase: "http://proxy.example/v1", PodcastVoices: hosts,
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return catalogue
}

// noService is a deployment with no speech service, and failing is a
// catalogue that answers with the given error.
type failing struct{ err error }

func (f failing) Roster(context.Context) (*voices.Roster, error) { return nil, f.err }

func opener(t testing.TB, rows *podcastRows, catalogue VoiceCatalogue) Podcasts {
	t.Helper()
	p := Podcasts{
		Rows: rows, Voices: catalogue, Brief: PodcastBrief{Speakers: 2, MinSeconds: 20, MaxSeconds: 30},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := p.Check(); err != nil {
		t.Fatal(err)
	}
	return p
}

// The row a podcast is opened as: at the brief gate, in the turn's
// account and thread, with the brief the podcast routes would propose.
func TestThePodcastRow(t *testing.T) {
	rows := &podcastRows{}
	owner := Owner{ThreadID: 3, UserID: "u-1"}
	id, err := opener(t, rows, kokoro(t, "af_heart", "am_adam")).Open(context.Background(), owner, PodcastRequest{Title: "Mitosis", Source: "Four phases.", Focus: "Keep it light."})
	if err != nil || id != 61 {
		t.Fatalf("id %d, err %v", id, err)
	}
	made := rows.created[0]
	if made.Title != "Mitosis" || made.SourceContent != "Four phases." || rows.createdFor[0] != "u-1" || made.ThreadID != 3 {
		t.Errorf("row = %+v", made)
	}
	// Byte for byte what internal/podcasts' own proposal writes for a
	// account with no earlier podcast on a deployment whose hosts are
	// these two: the web's brief form parses this.
	want := `{"language":"en","style":"conversational","speakers":[` +
		`{"slot":0,"name":"Host","role":"host","voice_id":"kokoro:af_heart","delivery":null},` +
		`{"slot":1,"name":"Guest","role":"guest","voice_id":"kokoro:am_adam","delivery":null}],` +
		`"duration":{"min_seconds":20,"max_seconds":30},"focus":"Keep it light."}`
	if string(made.Spec) != want {
		t.Errorf("brief\n got %s\nwant %s", made.Spec, want)
	}
	if len(rows.asked) != 1 || rows.asked[0] != "u-1" {
		t.Errorf("the last brief was read for %v", rows.asked)
	}
}

func briefOf(t *testing.T, spec []byte) (language string, cast []string, focus any) {
	t.Helper()
	var brief struct {
		Language string `json:"language"`
		Speakers []struct {
			Name    string `json:"name"`
			Role    string `json:"role"`
			VoiceID string `json:"voice_id"`
		} `json:"speakers"`
		Focus any `json:"focus"`
	}
	if err := json.Unmarshal(spec, &brief); err != nil {
		t.Fatal(err)
	}
	for _, speaker := range brief.Speakers {
		cast = append(cast, speaker.Name+" "+speaker.Role+" "+speaker.VoiceID)
	}
	return brief.Language, cast, brief.Focus
}

// lastBrief is a stored brief as the rows hand it over: the column's JSON,
// or nothing for an account with no podcast.
func lastBrief(t testing.TB, written string) []byte {
	t.Helper()
	if written != "" && !json.Valid([]byte(written)) {
		t.Fatalf("the test's own brief is not JSON: %s", written)
	}
	return []byte(written)
}

// The proposal's rules, each with one outcome. They are internal/podcasts'
// rules (proposal.go), and the cases are the ones its own tests pin.
func TestTheProposedBrief(t *testing.T) {
	for name, c := range map[string]struct {
		last     string
		hosts    []string
		speakers int
		language string
		cast     []string
	}{
		"no earlier podcast and no configured hosts: the roster's order": {"", nil, 2, "en", []string{"Host host kokoro:am_adam", "Guest guest kokoro:af_bella"}},
		"last time's language and voice, then one nobody has": {
			`{"language":"es","speakers":[{"slot":0,"voice_id":"kokoro:ef_dora"}]}`, nil, 2,
			"es", []string{"Host host kokoro:ef_dora", "Guest guest kokoro:em_alex"},
		},
		"last time's voices in their order": {
			`{"language":"it","speakers":[{"voice_id":"kokoro:im_nicola"},{"voice_id":"kokoro:if_sara"}]}`, nil, 2,
			"it", []string{"Host host kokoro:im_nicola", "Guest guest kokoro:if_sara"},
		},
		"a language with one voice gives it to both": {
			`{"language":"fr","speakers":[{"voice_id":"kokoro:ff_siwis"}]}`, nil, 2,
			"fr", []string{"Host host kokoro:ff_siwis", "Guest guest kokoro:ff_siwis"},
		},
		"a language the roster cannot voice starts again in English": {
			`{"language":"tlh","speakers":[{"voice_id":"kokoro:xx"}]}`, []string{"af_heart", "am_adam"}, 2,
			"en", []string{"Host host kokoro:af_heart", "Guest guest kokoro:am_adam"},
		},
		"a brief that cannot be read costs the defaults, not the podcast": {
			`{"language":"es","speakers":[{"voice_id":7}]}`, []string{"af_heart", "am_adam"}, 2,
			"en", []string{"Host host kokoro:af_heart", "Guest guest kokoro:am_adam"},
		},
		"the configured hosts, in their order": {
			"", []string{"am_michael", "af_bella"}, 2,
			"en", []string{"Host host kokoro:am_michael", "Guest guest kokoro:af_bella"},
		},
		"six speakers, the sixth a numbered guest": {
			"", []string{"af_heart", "am_adam"}, 6, "en", []string{
				"Host host kokoro:af_heart", "Guest guest kokoro:am_adam", "Expert expert kokoro:am_michael",
				"Co-Host cohost kokoro:af_bella", "Narrator narrator kokoro:af_nicole", "Guest 5 guest kokoro:af_sarah",
			},
		},
	} {
		rows := &podcastRows{}
		if c.last != "" {
			rows.lastBrief = lastBrief(t, c.last)
		}
		p := opener(t, rows, kokoro(t, c.hosts...))
		p.Brief.Speakers = c.speakers
		if _, err := p.Open(context.Background(), Owner{ThreadID: 3, UserID: "u"}, PodcastRequest{Title: "T", Source: "S"}); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		language, cast, focus := briefOf(t, rows.created[0].Spec)
		if language != c.language || strings.Join(cast, "|") != strings.Join(c.cast, "|") || focus != nil {
			t.Errorf("%s: %s %v focus %v", name, language, cast, focus)
		}
	}
}

// A language asked for (the studio audio dialog's) is the brief's, ahead
// of the account's last; the voices are that language's, and last time's
// voices in another language are not kept. A language no voice speaks is
// refused, not swapped for English.
func TestAnAskedLanguageIsTheBriefsAndItsVoicesAreCast(t *testing.T) {
	for name, c := range map[string]struct {
		last, asked string
		hosts       []string
		language    string
		cast        []string
	}{
		"asked, with no earlier podcast: the language's own voices": {
			"", "es", []string{"af_heart", "am_adam"},
			"es", []string{"Host host kokoro:em_alex", "Guest guest kokoro:ef_dora"},
		},
		"asked over last time's language, whose voices are not kept": {
			`{"language":"it","speakers":[{"voice_id":"kokoro:im_nicola"},{"voice_id":"kokoro:if_sara"}]}`, "es", nil,
			"es", []string{"Host host kokoro:em_alex", "Guest guest kokoro:ef_dora"},
		},
		"asked and the same as last time: last time's voices stay": {
			`{"language":"es","speakers":[{"voice_id":"kokoro:ef_dora"},{"voice_id":"kokoro:em_alex"}]}`, "es", nil,
			"es", []string{"Host host kokoro:ef_dora", "Guest guest kokoro:em_alex"},
		},
	} {
		rows := &podcastRows{}
		if c.last != "" {
			rows.lastBrief = lastBrief(t, c.last)
		}
		p := opener(t, rows, kokoro(t, c.hosts...))
		if _, err := p.Open(context.Background(), Owner{ThreadID: 3, UserID: "u"}, PodcastRequest{Title: "T", Source: "S", Language: c.asked}); err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		language, cast, _ := briefOf(t, rows.created[0].Spec)
		if language != c.language || strings.Join(cast, "|") != strings.Join(c.cast, "|") {
			t.Errorf("%s: %s %v", name, language, cast)
		}
	}

	rows := &podcastRows{}
	_, err := opener(t, rows, kokoro(t)).Open(context.Background(), Owner{ThreadID: 3, UserID: "u"},
		PodcastRequest{Title: "T", Source: "S", Language: "tlh"})
	var refused *PodcastRefusal
	if !errors.As(err, &refused) || refused.Reason != noVoiceForLanguage || len(rows.created) != 0 {
		t.Errorf("a language no voice speaks: err %v, %d rows", err, len(rows.created))
	}
}

func TestAPodcastIsOpenedForTheAskerAndWhen(t *testing.T) {
	ctx := context.Background()
	owner := Owner{ThreadID: 3, UserID: "u"}
	request := PodcastRequest{Title: "T", Source: "S"}
	refusedWith := func(err error) string {
		var refused *PodcastRefusal
		if errors.As(err, &refused) {
			return refused.Reason
		}
		return ""
	}

	// The podcast is the asker's own: the last brief is read for the
	// turn's account and the row is written for it, and no other account
	// is named anywhere.
	rows := &podcastRows{}
	if _, err := opener(t, rows, kokoro(t)).Open(ctx, owner, request); err != nil || len(rows.created) != 1 {
		t.Fatalf("the asker's own podcast: %v, %d rows", err, len(rows.created))
	}
	if len(rows.asked) != 1 || rows.asked[0] != owner.UserID || rows.createdFor[0] != owner.UserID {
		t.Errorf("asked for %v and created for %v, want only %q", rows.asked, rows.createdFor, owner.UserID)
	}

	// The three ways the voices can be unavailable are three sentences,
	// and none of them writes a row or asks the database anything.
	for name, c := range map[string]struct {
		err  error
		says string
	}{
		"no speech service":   {voices.ErrNotConfigured, noSpeechService},
		"the proxy is down":   {&voices.UnreachableError{Cause: errors.New("dial tcp 10.0.0.9: refused")}, voicesUnavailable},
		"a wrong setting":     {&voices.MisconfiguredError{Setting: "X", Reason: "is wrong"}, voicesMisconfigured},
		"something else":      {errors.New("boom"), ""},
		"a stopped turn":      {context.Canceled, ""},
		"a wrapped not-setup": {errors.Join(errors.New("while starting"), voices.ErrNotConfigured), noSpeechService},
	} {
		rows := &podcastRows{}
		_, err := opener(t, rows, failing{c.err}).Open(ctx, owner, request)
		if err == nil || refusedWith(err) != c.says || len(rows.created) != 0 || len(rows.asked) != 0 {
			t.Errorf("%s: %v (asked %v)", name, err, rows.asked)
		}
	}

	// A row that cannot be written is an error, not a refusal, and leaves
	// nothing behind.
	rows = &podcastRows{failCreate: errors.New("disk full")}
	if _, err := opener(t, rows, kokoro(t)).Open(ctx, owner, request); err == nil || refusedWith(err) != "" || len(rows.created) != 0 {
		t.Errorf("a failed insert: %v", err)
	}
}

func TestThePodcastOpenerRefusesToStartUnconfigured(t *testing.T) {
	good := opener(t, &podcastRows{}, kokoro(t))
	for name, change := range map[string]func(*Podcasts){
		"no database":        func(p *Podcasts) { p.Rows = nil },
		"no voices":          func(p *Podcasts) { p.Voices = nil },
		"no logger":          func(p *Podcasts) { p.Logger = nil },
		"no speakers":        func(p *Podcasts) { p.Brief.Speakers = 0 },
		"seven speakers":     func(p *Podcasts) { p.Brief.Speakers = 7 },
		"too short":          func(p *Podcasts) { p.Brief.MinSeconds = 14 },
		"longer than a day":  func(p *Podcasts) { p.Brief.MaxSeconds = 86401 },
		"a reversed length":  func(p *Podcasts) { p.Brief.MinSeconds, p.Brief.MaxSeconds = 60, 30 },
		"no length at all":   func(p *Podcasts) { p.Brief.MinSeconds, p.Brief.MaxSeconds = 0, 0 },
		"an unset brief":     func(p *Podcasts) { p.Brief = PodcastBrief{} },
		"speakers below one": func(p *Podcasts) { p.Brief.Speakers = -1 },
	} {
		p := good
		change(&p)
		if err := p.Check(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
