package videojob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
	"git.danvics.com/danvics/soloclef/soloclef_studio/artifactsave"
	"git.danvics.com/danvics/soloclef/soloclef_studio/deckspec"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// fakeRaster draws every frame as the same picture: a gradient with a
// few bands, so a frame sampled from the video is not blank, encoded once.
// render, when set, runs before each frame (a test that cancels mid-render
// sets it).
type fakeRaster struct {
	mu     sync.Mutex
	drawn  int
	render func(ctx context.Context)
	png    []byte
}

func newFakeRaster(t *testing.T) *fakeRaster {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	for y := 0; y < 1080; y++ {
		for x := 0; x < 1920; x++ {
			picture.Set(x, y, color.RGBA{uint8(x / 8), uint8(y / 5), uint8((x + y) / 12), 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return &fakeRaster{png: encoded.Bytes()}
}

func (f *fakeRaster) PNG(ctx context.Context, svg, out string) error {
	f.mu.Lock()
	f.drawn++
	hook := f.render
	f.mu.Unlock()
	if hook != nil {
		hook(ctx)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !strings.HasPrefix(svg, "<svg") {
		return errors.New("not an SVG")
	}
	return os.WriteFile(out, f.png, 0o600)
}

// blankPNG is a frame of one colour: a slide that drew nothing.
func blankPNG(t *testing.T) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, 1920, 1080))
	for y := 0; y < 1080; y++ {
		for x := 0; x < 1920; x++ {
			picture.Set(x, y, color.RGBA{250, 248, 242, 255})
		}
	}
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, picture); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func (f *fakeRaster) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.drawn
}

// fakeRows is the job's row in memory, with the state machine's guards.
type fakeRows struct {
	mu        sync.Mutex
	job       Claimed
	status    string
	code      string
	internal  string
	phases    []string
	delivered *artifactsave.Rows
	// onPhase runs on every heartbeat, after it is recorded.
	onPhase func(phase string)
}

func newFakeRows(request Request) *fakeRows {
	thread := request.RootThreadID
	column, _ := json.Marshal(map[string]any{"version": 1, "brief": request.Brief, "source_references": []string{},
		"root_thread_id": request.RootThreadID, "source_material": "Heart: four chambers.\n\nValves: four."})
	return &fakeRows{status: "queued", job: Claimed{
		ID: 7, UserID: "aaaaaaaa-0000-4000-8000-000000000001", Kind: "video", Title: "The heart", ToolCallID: "call-1",
		ThreadID: &thread, TaskID: "deliverable-job:7:attempt:1", Attempt: 1, Request: column,
	}}
}

func (r *fakeRows) Claim(_ context.Context, _ int64, taskID string, _ ClaimRules) (Claim, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if taskID != r.job.TaskID || r.status != "queued" {
		return Claim{Why: "not ours"}, nil
	}
	r.status = "running"
	return Claim{Outcome: claimRun, Job: r.job}, nil
}

func (r *fakeRows) Heartbeat(_ context.Context, _ Claimed, phase string, _ int) (bool, error) {
	r.mu.Lock()
	ours := r.status == "running"
	if ours {
		r.phases = append(r.phases, phase)
	}
	hook := r.onPhase
	r.mu.Unlock()
	if ours && hook != nil {
		hook(phase)
	}
	return ours, nil
}

func (r *fakeRows) Touch(context.Context, Claimed) error { return nil }

func (r *fakeRows) State(context.Context, Claimed) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status, r.job.TaskID, nil
}

func (r *fakeRows) setStatus(status string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = status
}

func (r *fakeRows) Fail(_ context.Context, _ Claimed, code, internal string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch r.status {
	case "cancelling":
		r.status, r.code = "cancelled", "cancelled"
	case "running":
		r.status, r.code, r.internal = "failed", code, internal
	}
	return nil
}

func (r *fakeRows) Cancelled(context.Context, Claimed) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == "cancelling" {
		r.status, r.code = "cancelled", "cancelled"
	}
	return nil
}

func (r *fakeRows) Release(_ context.Context, job Claimed, rules ClaimRules) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status != "running" {
		return false, nil
	}
	if job.Takeovers >= rules.MaxTakeovers {
		r.status = "failed"
		return false, nil
	}
	r.status = "queued"
	return true, nil
}

func (r *fakeRows) ReserveArtifactID(context.Context) (int64, error) { return 55, nil }

func (r *fakeRows) Deliver(_ context.Context, _ Claimed, artifact artifactsave.Rows) (artifactsave.Created, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status != "running" {
		return artifactsave.Created{}, false, nil
	}
	r.status, r.delivered = "ready", &artifact
	return artifactsave.Created{ArtifactID: artifact.ID, DocumentID: 155, FileID: 255}, true, nil
}

func (r *fakeRows) snapshot() (string, string, *artifactsave.Rows) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.status, r.code, r.delivered
}

// fakeVoice speaks every line as the same real MP3 (a tone ffmpeg made,
// which the join decodes), and measures a line at two and a half words a
// second: the length the film is planned to, whatever the tone's own. It
// joins a slide's passages as audiofile.JoinSpaced does in length: the
// parts and a gap after each but the last, one part returned as it is.
type fakeVoice struct {
	mu    sync.Mutex
	lines []speech.Line
	tone  []byte
	// wordsPerSecond slows or speeds the measured narration.
	wordsPerSecond float64
	// seconds is what each answer and each join measures, by its bytes.
	seconds map[string]float64
	// joins is every join's gap and number of parts.
	joins []fakeJoin
}

type fakeJoin struct {
	parts int
	gap   float64
}

func newFakeVoice(t *testing.T) *fakeVoice {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tone.mp3")
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=330:duration=1.5", "-ac", "1", "-ar", "24000", "-b:a", "48k", path).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	tone, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return &fakeVoice{tone: tone, seconds: map[string]float64{}}
}

// tagged is the tone with a tag after it, so each answer's bytes differ
// and Seconds can tell which it is measuring.
func (v *fakeVoice) tagged(tag string) []byte {
	return append(append([]byte(nil), v.tone...), []byte(tag)...)
}

func (v *fakeVoice) Speak(_ context.Context, line speech.Line) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lines = append(v.lines, line)
	rate := v.wordsPerSecond
	if rate == 0 {
		rate = 2.5
	}
	data := v.tagged(fmt.Sprintf("#line-%d", len(v.lines)))
	v.seconds[string(data)] = float64(len(strings.Fields(line.Text))) / rate
	return data, nil
}

func (v *fakeVoice) Seconds(_ context.Context, data []byte) (float64, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.seconds[string(data)], nil
}

func (v *fakeVoice) JoinSpaced(_ context.Context, parts [][]byte, gap float64) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.joins = append(v.joins, fakeJoin{parts: len(parts), gap: gap})
	if len(parts) == 1 {
		return parts[0], nil
	}
	total := gap * float64(len(parts)-1)
	for _, part := range parts {
		total += v.seconds[string(part)]
	}
	data := v.tagged(fmt.Sprintf("#join-%d", len(v.joins)))
	v.seconds[string(data)] = total
	return data, nil
}

// voicedEvenly is a narration of seconds a slide, its passages starting
// evenly through it: a film to plan without voicing anything.
func voicedEvenly(video Authored, seconds float64) []voiced {
	voices := make([]voiced, len(video.Deck.Slides))
	for i, s := range video.Deck.Slides {
		steps := deckspec.Steps(s)
		starts := make([]float64, steps)
		for k := range starts {
			starts[k] = seconds * float64(k) / float64(steps)
		}
		voices[i] = voiced{Number: i + 1, Seconds: seconds, Starts: starts}
	}
	return voices
}

// fakeModels hands out one scripted model.
type fakeModels struct{ script *modeltest.Script }

func (m fakeModels) StudioModel(context.Context, string) (string, error) { return "chat-model", nil }
func (m fakeModels) Client(context.Context, string, string) (model.Model, error) {
	return m.script, nil
}

type fakeIndexer struct {
	mu        sync.Mutex
	documents []int64
}

func (f *fakeIndexer) Index(_ context.Context, documentID int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.documents = append(f.documents, documentID)
	return nil
}

// deckReply is an author's reply as models write it, in a code fence.
func deckReply(first, second []string) string { return "```json\n" + deckJSON(first, second) + "\n```" }

// deckJSON is a two-slide video, a bullets slide and a stats slide,
// narrated with these passages each.
func deckJSON(first, second []string) string {
	deck := map[string]any{
		"title": "The heart", "theme": "classic",
		"slides": []any{
			map[string]any{"kind": "bullets", "title": "The heart has four chambers",
				"bullets": []string{"**Atria:** receive blood", "**Ventricles:** pump it out"}, "narration": first},
			map[string]any{"kind": "stats", "title": "A lifetime of beats",
				"stats":     []any{map[string]any{"value": "72", "label": "beats a minute at rest"}, map[string]any{"value": "5 L", "label": "pumped every minute"}},
				"narration": second},
		},
	}
	encoded, _ := json.Marshal(deck)
	return string(encoded)
}

// rig is a job wired to fakes.
type rig struct {
	job    *Job
	rows   *fakeRows
	raster *fakeRaster
	voice  *fakeVoice
	script *modeltest.Script
	files  *filestore.Local
	dir    string
	index  *fakeIndexer
	// notified is every ending the job reported to the owner.
	notified *recordingNotifier
	stages   []Stage
}

func newRig(t *testing.T, replies ...string) *rig {
	t.Helper()
	for _, program := range []string{"ffmpeg", "ffprobe"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Skipf("%s is not installed here; the video is joined and checked with it", program)
		}
	}
	dir := t.TempDir()
	files, err := filestore.OpenLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	scripted := make([]modeltest.Reply, len(replies))
	for i, reply := range replies {
		scripted[i] = modeltest.Reply{Text: reply}
	}
	r := &rig{
		rows: newFakeRows(Request{Brief: "The heart, for students", RootThreadID: 3}), raster: newFakeRaster(t),
		voice: newFakeVoice(t), script: modeltest.NewScript(scripted...), files: files, dir: dir, index: &fakeIndexer{},
		notified: &recordingNotifier{},
	}
	r.job = &Job{
		Rows: r.rows, Raster: r.raster, Models: fakeModels{r.script}, Voicer: r.voice, Audio: r.voice,
		Files: files, Indexer: r.index, Notifier: r.notified,
		Config: Config{Enabled: true, Resvg: "resvg", FrameWorkers: 4, RenderTimeout: time.Hour, MaxRenders: 1,
			Voice: "marin", VoiceID: "openai:marin", NarrationInstructions: "unhurried"},
		Timing: Timing{CallTimeout: time.Minute, StatusInterval: 5 * time.Millisecond, KeepAlive: 20 * time.Millisecond,
			Claim: ClaimRules{StaleAfter: time.Second, MaxTakeovers: 2}, NarrationConcurrency: 3},
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		Measured: func(_ int64, stages []Stage) { r.stages = stages },
	}
	if err := r.job.Check(); err != nil {
		t.Fatal(err)
	}
	return r
}

// storedObjects is every object the local store holds.
func (r *rig) storedObjects(t *testing.T) []string {
	t.Helper()
	var keys []string
	_ = walk(r.dir, func(path string) { keys = append(keys, strings.TrimPrefix(path, r.dir+"/")) })
	return keys
}

func walk(dir string, visit func(string)) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		full := dir + "/" + entry.Name()
		if entry.IsDir() {
			_ = walk(full, visit)
		} else if !strings.HasPrefix(entry.Name(), ".") {
			visit(full)
		}
	}
	return nil
}

// recordingNotifier keeps every Studio run the job reported.
type recordingNotifier struct {
	mu   sync.Mutex
	runs []notify.StudioRun
}

func (n *recordingNotifier) StudioFinished(_ context.Context, run notify.StudioRun) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.runs = append(n.runs, run)
	return nil
}

func (n *recordingNotifier) reported() []notify.StudioRun {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]notify.StudioRun(nil), n.runs...)
}
