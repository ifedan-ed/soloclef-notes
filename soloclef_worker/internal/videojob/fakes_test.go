package videojob

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_studio/artifactsave"
	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// fakeBox is a sandbox that answers the harness's commands as the real one
// does, from files it keeps in memory. A test changes one answer by setting
// a hook.
type fakeBox struct {
	mu       sync.Mutex
	files    map[string][]byte
	commands []string
	// preflight, stills and render answer those commands: "" is success,
	// anything else is printed with exit 1.
	preflight, stills func() string
	// layout answers the layout check of each --stills call (counted from
	// 0): each scene's findings by its number, nil for a clean frame. With
	// noLayout the stills step writes no report at all.
	layout   func(call int) map[int][]string
	noLayout bool
	stillsAt int
	// render runs while a full render "renders"; a test blocks in it.
	render func(ctx context.Context)
	// findings make the probe say what is wrong with the video.
	badResolution bool
	// blankScene makes the last scene's frame one colour.
	blankScene bool
	terminated []string
}

func newFakeBox() *fakeBox {
	return &fakeBox{files: map[string][]byte{}, preflight: func() string { return "" }, stills: func() string { return "" }}
}

func (b *fakeBox) RunWithin(ctx context.Context, command string, limit time.Duration) (sandbox.Result, error) {
	if limit <= 0 {
		return sandbox.Result{}, errors.New("no limit")
	}
	b.mu.Lock()
	b.commands = append(b.commands, command)
	b.mu.Unlock()
	ok := sandbox.Result{}
	refused := func(text string) sandbox.Result { return sandbox.Result{Output: text, ExitCode: 1} }
	switch {
	case strings.Contains(command, "cp -a /opt/remotion/."):
		return ok, nil
	case strings.Contains(command, "--preflight"):
		if text := b.preflight(); text != "" {
			return refused(text), nil
		}
		return sandbox.Result{Output: `{"ok":true}`}, nil
	case strings.Contains(command, "--stills"):
		if text := b.stills(); text != "" {
			return refused(text), nil
		}
		// node render.mjs '--stills' '<props>' '<dir>': the report is
		// written beside the stills, one entry per scene of the props.
		words := strings.Fields(command)
		props := strings.Trim(words[len(words)-2], "'")
		dir := strings.Trim(words[len(words)-1], "'")
		var written project
		if err := json.Unmarshal(b.get(props), &written); err != nil {
			return refused("props.json is not readable"), nil
		}
		b.mu.Lock()
		call := b.stillsAt
		b.stillsAt++
		b.mu.Unlock()
		if b.noLayout {
			return ok, nil
		}
		var found map[int][]string
		if b.layout != nil {
			found = b.layout(call)
		}
		scenes := []map[string]any{}
		for _, scene := range written.Scenes {
			findings := append([]string{}, found[scene.Number]...)
			scenes = append(scenes, map[string]any{"slide_number": scene.Number, "findings": findings})
		}
		report, _ := json.Marshal(map[string]any{"scenes": scenes})
		b.put(dir+"/layout.json", report)
		return ok, nil
	case strings.Contains(command, "node render.mjs"):
		if b.render != nil {
			b.render(ctx)
			if ctx.Err() != nil {
				return sandbox.Result{}, ctx.Err()
			}
		}
		words := strings.Fields(command)
		output := strings.Trim(words[len(words)-1], "'")
		b.put(output, []byte("an mp4 of the scenes"))
		b.put(output+".segments.json", []byte(`{"expected_duration_seconds": 12.0}`))
		return ok, nil
	case strings.HasPrefix(command, "ffprobe"):
		width := 1920
		if b.badResolution {
			width = 1280
		}
		return sandbox.Result{Output: fmt.Sprintf(`{"format":{"duration":"12.000"},"streams":[{"codec_type":"video","width":%d,"height":1080,"duration":"12.0","nb_read_packets":"360"},{"codec_type":"audio","duration":"12.0","nb_read_packets":"560"}]}`, width)}, nil
	case strings.Contains(command, "statistics.pstdev"):
		var lines []string
		for range strings.Fields(strings.SplitN(strings.TrimPrefix(command, "for at in "), "; do", 2)[0]) {
			lines = append(lines, `{"levels": 120, "stddev": 33.5}`)
		}
		if b.blankScene {
			lines[len(lines)-1] = `{"levels": 1, "stddev": 0}`
		}
		return sandbox.Result{Output: strings.Join(lines, "\n") + "\n"}, nil
	case strings.HasPrefix(command, "sha256sum"):
		path := strings.Trim(strings.TrimPrefix(command, "sha256sum -- "), "'")
		sum := sha256.Sum256(b.get(path))
		return sandbox.Result{Output: hex.EncodeToString(sum[:]) + "  " + path + "\n"}, nil
	}
	return refused("the fake sandbox has no answer for: " + command), nil
}

func (b *fakeBox) put(path string, data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.files[path] = data
}

func (b *fakeBox) get(path string) []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.files[path]
}

func (b *fakeBox) ReadFile(_ context.Context, path string, limit int64) ([]byte, error) {
	data := b.get(path)
	if data == nil {
		return nil, sandbox.ErrNotFound
	}
	if int64(len(data)) > limit {
		return nil, sandbox.ErrTooLarge
	}
	return data, nil
}

func (b *fakeBox) WriteFile(_ context.Context, path string, data []byte) error {
	b.put(path, append([]byte(nil), data...))
	return nil
}

func (b *fakeBox) OpenFile(_ context.Context, path string) (io.ReadCloser, error) {
	data := b.get(path)
	if data == nil {
		return nil, sandbox.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(data)), nil
}

// count is how many commands contained text.
func (b *fakeBox) count(text string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, command := range b.commands {
		if strings.Contains(command, text) {
			n++
		}
	}
	return n
}

type fakeBoxes struct{ box *fakeBox }

func (f fakeBoxes) ForJob(context.Context, string, string) (Sandbox, error) { return f.box, nil }
func (f fakeBoxes) TerminateJob(_ context.Context, name string) error {
	f.box.mu.Lock()
	defer f.box.mu.Unlock()
	f.box.terminated = append(f.box.terminated, name)
	return nil
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

func (r *fakeRows) Release(context.Context, Claimed) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.status == "running" {
		r.status = "queued"
	}
	return nil
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

// fakeVoice speaks a line as bytes that name it, and measures it at two
// and a half words a second.
type fakeVoice struct {
	mu    sync.Mutex
	lines []speech.Line
	// slow makes every line take this long.
	wordsPerSecond float64
}

func (v *fakeVoice) Speak(_ context.Context, line speech.Line) ([]byte, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.lines = append(v.lines, line)
	return []byte("voice:" + line.Text), nil
}

func (v *fakeVoice) Seconds(_ context.Context, data []byte) (float64, error) {
	rate := v.wordsPerSecond
	if rate == 0 {
		rate = 2.5
	}
	return float64(len(strings.Fields(strings.TrimPrefix(string(data), "voice:")))) / rate, nil
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

// sceneCode is a scene module as an author writes one.
func sceneCode(heading string) string {
	return `import {AbsoluteFill, useCurrentFrame, useVideoConfig} from "remotion";
` + staggerImport + `
export default function Scene() {
  const frame = useCurrentFrame();
  const {fps} = useVideoConfig();
  return (
    <AbsoluteFill style={{padding: 100, background: "#0b1f33", color: "white", fontFamily: "Inter", justifyContent: "center"}}>
      <h1 style={{fontSize: 96, ...stagger(frame, fps, 0, 2)}}>` + heading + `</h1>
      <p style={{fontSize: 52, ...stagger(frame, fps, 1, 2)}}>Four chambers, four valves.</p>
    </AbsoluteFill>
  );
}
`
}

// draftReply is an author's reply of scenes with these transcripts.
func draftReply(transcripts ...string) string {
	scenes := make([]map[string]string, len(transcripts))
	for i, transcript := range transcripts {
		heading := fmt.Sprintf("Part %d", i+1)
		scenes[i] = map[string]string{"narration": transcript, "onScreenMarkdown": "# " + heading + "\n\n- Four chambers", "tsx": sceneCode(heading)}
	}
	encoded, _ := json.Marshal(map[string]any{"language": "en", "scenes": scenes})
	return "```json\n" + string(encoded) + "\n```"
}

func repairReply(count int) string {
	scenes := make([]map[string]string, count)
	for i := range scenes {
		scenes[i] = map[string]string{"on_screen_markdown": "# Repaired", "code": sceneCode("Repaired")}
	}
	encoded, _ := json.Marshal(map[string]any{"scenes": scenes})
	return string(encoded)
}

// rig is a job wired to fakes.
type rig struct {
	job    *Job
	rows   *fakeRows
	box    *fakeBox
	voice  *fakeVoice
	script *modeltest.Script
	files  *filestore.Local
	dir    string
	index  *fakeIndexer
	stages []Stage
}

func newRig(t *testing.T, replies ...string) *rig {
	t.Helper()
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
		rows: newFakeRows(Request{Brief: "The heart, for students", RootThreadID: 3}), box: newFakeBox(),
		voice: &fakeVoice{}, script: modeltest.NewScript(scripted...), files: files, dir: dir, index: &fakeIndexer{},
	}
	r.job = &Job{
		Rows: r.rows, Sandboxes: fakeBoxes{r.box}, Models: fakeModels{r.script}, Voicer: r.voice, Audio: r.voice,
		Files: files, Indexer: r.index,
		Config: Config{Enabled: true, Mode: ModeFrames, RenderTimeout: time.Hour, RenderConcurrency: 2,
			FramesPerSegment: 1800, FrameTimeout: 7 * time.Second, MaxRenders: 1,
			Voice: "marin", VoiceID: "openai:marin", NarrationInstructions: "unhurried",
			Sandbox: sandbox.Settings{CommandTimeout: time.Minute}},
		Timing: Timing{CallTimeout: time.Minute, StatusInterval: 5 * time.Millisecond, KeepAlive: 20 * time.Millisecond,
			Claim: ClaimRules{StaleAfter: time.Second, MaxTakeovers: 2}, RepairCycles: 2, NarrationConcurrency: 3},
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

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }
