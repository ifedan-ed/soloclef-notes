package podcast

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// memoryRows is Rows in memory, with the same guards the SQL has: a write
// lands only from the status it expects.
type memoryRows struct {
	mu        sync.Mutex
	run       Run
	reason    string
	delivered *Delivery
	// onStatus is called on every Status probe, so a test can cancel the
	// podcast at a chosen moment.
	onStatus func(*Run)
}

func (m *memoryRows) Load(_ context.Context, id int64) (Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run.ID != id {
		return Run{}, ErrGone
	}
	return m.run, nil
}

func (m *memoryRows) Status(context.Context, int64, string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.onStatus != nil {
		m.onStatus(&m.run)
	}
	return m.run.Status, nil
}

func (m *memoryRows) set(status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.run.Status = status
}

func (m *memoryRows) AttachTranscript(_ context.Context, _ int64, _ string, transcript []byte) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run.Status != statusDrafting {
		return false, nil
	}
	m.run.Status, m.run.Transcript = statusRendering, transcript
	return true, nil
}

func (m *memoryRows) Deliver(_ context.Context, delivery Delivery) (int64, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run.Status != statusRendering {
		return 0, false, nil
	}
	m.run.Status = statusReady
	m.delivered = &delivery
	return 77, true, nil
}

func (m *memoryRows) Fail(_ context.Context, _ int64, _ string, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.run.Status == statusDrafting || m.run.Status == statusRendering {
		m.run.Status, m.reason = "failed", reason
	}
	return nil
}

// scriptedModel answers the outline call with a plan of `beats` beats and
// every segment call with two lines, one per speaker.
type scriptedModel struct {
	mu      sync.Mutex
	beats   int
	calls   int
	onCall  func(call int)
	refuse  bool
	systems []string
}

func (s *scriptedModel) Generate(_ context.Context, request model.Request, _ func(model.Chunk)) (model.Response, error) {
	s.mu.Lock()
	s.calls++
	call := s.calls
	system := request.Messages[0].Content
	s.systems = append(s.systems, system)
	s.mu.Unlock()
	if s.onCall != nil {
		s.onCall(call)
	}
	if s.refuse {
		return model.Response{}, &model.Error{Status: 400, Message: "no"}
	}
	var reply string
	if strings.Contains(system, "showrunner") {
		var segments []string
		for i := 0; i < s.beats; i++ {
			segments = append(segments, fmt.Sprintf(`{"title":"Beat %d","talking_points":["point"],"target_words":120}`, i+1))
		}
		reply = `{"segments":[` + strings.Join(segments, ",") + `]}`
	} else {
		reply = fmt.Sprintf(`{"turns":[{"speaker":0,"text":"Line %d from the host.","emotion":"warm"},{"speaker":1,"text":"Line %d from the guest."},{"speaker":9,"text":"A slot nobody has."}]}`, call, call)
	}
	return model.Response{Message: model.Message{Role: model.RoleAssistant, Content: reply}}, nil
}

type fakeModels struct{ model *scriptedModel }

func (f fakeModels) StudioModel(context.Context, string) (string, error) { return "chat-model", nil }
func (f fakeModels) Client(context.Context, string, string) (model.Model, error) {
	return f.model, nil
}

// countingVoicer answers every line with the same bytes and counts.
type countingVoicer struct {
	mu     sync.Mutex
	lines  []speech.Line
	before func(count int)
	fail   error
}

func (c *countingVoicer) Speak(_ context.Context, line speech.Line) ([]byte, error) {
	c.mu.Lock()
	c.lines = append(c.lines, line)
	count := len(c.lines)
	c.mu.Unlock()
	if c.before != nil {
		c.before(count)
	}
	if c.fail != nil {
		return nil, c.fail
	}
	return []byte("ID3 one line of audio"), nil
}

func (c *countingVoicer) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.lines)
}

// joiningAudio concatenates the files and calls each line a second.
type joiningAudio struct{}

func (joiningAudio) JoinMP3(_ context.Context, segments []string, output string) error {
	var joined []byte
	for _, path := range segments {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		joined = append(joined, data...)
	}
	return os.WriteFile(output, joined, 0o600)
}

func (joiningAudio) Duration(_ context.Context, path string) (float64, error) {
	data, err := os.ReadFile(path)
	return float64(len(data)) / float64(len("ID3 one line of audio")), err
}

type recordingIndexer struct {
	mu        sync.Mutex
	documents []int64
}

func (r *recordingIndexer) Index(_ context.Context, documentID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.documents = append(r.documents, documentID)
	return nil
}

const twoSpeakerBrief = `{"language":"en","style":"conversational","speakers":[` +
	`{"slot":0,"name":"Ada","role":"host","voice_id":"openai:marin","delivery":"Bright."},` +
	`{"slot":1,"name":"Ben","role":"guest","voice_id":"openai:cedar","delivery":null}],` +
	`"duration":{"min_seconds":120,"max_seconds":300},"focus":null}`

type harness struct {
	rows    *memoryRows
	model   *scriptedModel
	voicer  *countingVoicer
	indexer *recordingIndexer
	job     *Job
	workdir string
}

func newHarness(t *testing.T, status string) *harness {
	t.Helper()
	h := &harness{
		rows: &memoryRows{run: Run{ID: 5, UserID: "owner", Status: status, Title: "Sepsis",
			Source: "# Sepsis\n\nSepsis is a dysregulated host response.", Spec: []byte(twoSpeakerBrief)}},
		model:   &scriptedModel{beats: 3},
		voicer:  &countingVoicer{},
		indexer: &recordingIndexer{},
		workdir: t.TempDir(),
	}
	h.job = &Job{
		Rows: h.rows, Models: fakeModels{h.model}, Voicer: h.voicer, Audio: joiningAudio{}, Indexer: h.indexer,
		Config: Config{CallTimeout: time.Second, Speech: speech.Service{Roster: speech.OpenAI, Model: "gpt-4o-mini-tts"},
			Instructions: "Calm.", Concurrency: 4, WorkDirectory: h.workdir, StatusInterval: time.Nanosecond},
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if err := h.job.Check(); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *harness) run(t *testing.T) error {
	t.Helper()
	return h.job.Run(context.Background(), &celery.Message{Task: Task, Args: []any{float64(5)}})
}

func (h *harness) transcript(t *testing.T) Transcript {
	t.Helper()
	var transcript Transcript
	if err := json.Unmarshal(h.rows.run.Transcript, &transcript); err != nil {
		t.Fatal(err)
	}
	return transcript
}

func (h *harness) workdirIsEmpty(t *testing.T) {
	t.Helper()
	entries, err := os.ReadDir(h.workdir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("the podcast's working directory was left behind: %v", entries)
	}
}
