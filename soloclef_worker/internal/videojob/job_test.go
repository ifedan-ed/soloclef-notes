package videojob

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

func message() *celery.Message {
	return &celery.Message{Task: Task, ID: "deliverable-job:7:attempt:1", Kwargs: map[string]any{"job_id": float64(7)}}
}

// A video goes from the request to a kept artifact: the MP4 as its primary
// file, each scene's narration stored, and the slides and scene codes the
// video route serves.
func TestAVideoIsMadeAndKeptInTheShapesItsReadersRead(t *testing.T) {
	r := newRig(t, draftReply("The heart has four chambers.", "Four valves keep blood moving one way."))
	if err := r.job.Run(context.Background(), message()); err != nil {
		t.Fatal(err)
	}
	status, _, artifact := r.rows.snapshot()
	if status != "ready" || artifact == nil {
		t.Fatalf("status %q, artifact %v", status, artifact)
	}
	if artifact.Format != "video" || artifact.Primary.MimeType != "video/mp4" || artifact.Primary.Filename != "The heart.mp4" ||
		!strings.HasPrefix(artifact.Primary.Key, "artifacts/"+r.rows.job.UserID+"/55/primary/") {
		t.Errorf("primary file %+v", artifact.Primary)
	}
	var meta struct {
		Verification map[string]any   `json:"verification"`
		Duration     float64          `json:"duration_seconds"`
		Slides       []map[string]any `json:"slides"`
		SceneCodes   []map[string]any `json:"scene_codes"`
	}
	if err := json.Unmarshal(artifact.Metadata, &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Verification["verified"] != true || meta.Duration != 12 || strings.Contains(string(artifact.Metadata), "still_review") {
		t.Errorf("metadata %s", artifact.Metadata)
	}
	if len(meta.Slides) != 2 || len(meta.SceneCodes) != 2 {
		t.Fatalf("slides %d, scene codes %d", len(meta.Slides), len(meta.SceneCodes))
	}
	first := meta.Slides[0]
	key, _ := first["audio_storage_key"].(string)
	if first["slide_number"] != float64(1) || first["title"] != "Part 1" || first["storage_backend"] != "local" ||
		!strings.HasPrefix(key, "artifacts/"+r.rows.job.UserID+"/55/slides/slide-1-") || first["duration_in_frames"] != float64(60) {
		t.Errorf("first slide %v", first)
	}
	if !strings.Contains(meta.SceneCodes[1]["code"].(string), "export default function Scene") {
		t.Errorf("second scene code %v", meta.SceneCodes[1])
	}
	objects := r.storedObjects(t)
	if len(objects) != 3 {
		t.Errorf("stored %v, want the video and two narrations", objects)
	}
	if !strings.Contains(artifact.Markdown, "# The heart") || !strings.Contains(artifact.Markdown, "Narration: The heart has four chambers.") || strings.Contains(artifact.Markdown, "**") {
		t.Errorf("markdown %q", artifact.Markdown)
	}
	if len(r.index.documents) != 1 || len(r.box.terminated) != 1 || r.box.terminated[0] != "deliverable-job-7-attempt-1" {
		t.Errorf("indexed %v, sandboxes removed %v", r.index.documents, r.box.terminated)
	}
	if len(r.voice.lines) != 2 || r.voice.lines[0].Voice != "marin" || r.voice.lines[0].Instructions != "unhurried" {
		t.Errorf("voiced %+v", r.voice.lines)
	}
	// The scenes' layout was checked once, and no model was asked to look:
	// the author's draft is the only model call.
	if r.box.count("--stills") != 1 || len(r.script.Requests()) != 1 {
		t.Errorf("stills %d, model calls %d", r.box.count("--stills"), len(r.script.Requests()))
	}
	var names []string
	for _, stage := range r.stages {
		names = append(names, stage.Name)
	}
	if got := strings.Join(names, ","); got != "sandbox,authoring,narration,preflight,stills,render,verify,save" {
		t.Errorf("stages measured: %s", got)
	}
}

// A finding of the layout check is shown to a repair, with its scene, and
// the repaired scenes are checked again before they are rendered.
func TestALayoutFindingIsRepairedAndCheckedAgain(t *testing.T) {
	r := newRig(t, draftReply("One.", "Two."), repairReply(2))
	r.box.layout = func(call int) map[int][]string {
		if call == 0 {
			return map[int][]string{2: {`text "the moon" has a line drawn over it`}}
		}
		return nil
	}
	if err := r.job.Run(context.Background(), message()); err != nil {
		t.Fatal(err)
	}
	if status, _, artifact := r.rows.snapshot(); status != "ready" || !strings.Contains(string(artifact.Metadata), "Repaired") {
		t.Fatalf("status %q", status)
	}
	requests := r.script.Requests()
	if len(requests) != 2 || !strings.Contains(requests[1].Messages[1].Content, `scene 2: text \"the moon\" has a line drawn over it`) {
		t.Errorf("the repair was not shown the finding: %d requests", len(requests))
	}
	if r.box.count("--preflight") != 2 || r.box.count("--stills") != 2 || r.box.count("node render.mjs '/workspace") != 1 {
		t.Errorf("checked %d, stills %d, rendered %d", r.box.count("--preflight"), r.box.count("--stills"), r.box.count("node render.mjs '/workspace"))
	}
}

// A stills step that leaves no layout report did not check the layout: the
// video fails rather than going on unchecked.
func TestAMissingLayoutReportFailsTheVideo(t *testing.T) {
	r := newRig(t, draftReply("One.", "Two."))
	r.box.noLayout = true
	err := r.job.Run(context.Background(), message())
	status, code, _ := r.rows.snapshot()
	if err == nil || status != "failed" || code != codeRender {
		t.Fatalf("status %q code %q (%v)", status, code, err)
	}
	if !strings.Contains(r.rows.internal, "layout check's report") || r.box.count("node render.mjs '/workspace") != 0 {
		t.Errorf("internal error %q; rendered %d", r.rows.internal, r.box.count("node render.mjs '/workspace"))
	}
}

// Every kind of finding draws on one budget of repairs, and a build that
// keeps failing ends the job once it is spent.
func TestRepairsShareOneBudget(t *testing.T) {
	r := newRig(t, draftReply("One.", "Two."), repairReply(2), repairReply(2))
	r.box.preflight = func() string { return "src/scenes/scene-1.tsx: Unexpected token" }
	err := r.job.Run(context.Background(), message())
	status, code, _ := r.rows.snapshot()
	if err == nil || status != "failed" || code != codeRender {
		t.Fatalf("status %q code %q (%v)", status, code, err)
	}
	if r.box.count("--preflight") != 3 || len(r.script.Requests()) != 3 {
		t.Errorf("built %d times with %d model calls", r.box.count("--preflight"), len(r.script.Requests()))
	}
	if !strings.Contains(r.rows.internal, "after 2 repairs") {
		t.Errorf("internal error %q", r.rows.internal)
	}
}

// A video that fails its checks after rendering is repaired, re-checked
// from the build and rendered again; one that still fails is marked
// verification_failed.
func TestAVideoThatFailsVerificationIsRepairedThenRefused(t *testing.T) {
	r := newRig(t, draftReply("One.", "Two."), repairReply(2), repairReply(2))
	r.box.badResolution = true
	if err := r.job.Run(context.Background(), message()); err == nil {
		t.Fatal("a video of the wrong size was kept")
	}
	if status, code, _ := r.rows.snapshot(); status != "failed" || code != codeVerification {
		t.Fatalf("status %q code %q", status, code)
	}
	if r.box.count("node render.mjs '/workspace") != 3 || r.box.count("--preflight") != 3 {
		t.Errorf("rendered %d times", r.box.count("node render.mjs '/workspace"))
	}
	if len(r.storedObjects(t)) != 0 {
		t.Errorf("a refused video left files: %v", r.storedObjects(t))
	}
}

// Narration measured over the limit is shortened once and voiced again; a
// second overrun is duration_limit, before anything is rendered.
func TestOverlongNarrationIsShortenedOnceThenRefused(t *testing.T) {
	long := strings.Repeat("word ", 400)
	r := newRig(t, draftReply(long, long), `{"scenes":[{"transcript":"`+long+`"},{"transcript":"`+long+`"}]}`)
	// Slow enough that 800 words overrun 600 seconds; and the word budget
	// (1,296) does not catch them before voicing.
	r.voice.wordsPerSecond = 1
	if err := r.job.Run(context.Background(), message()); err == nil {
		t.Fatal("an overlong video was made")
	}
	status, code, _ := r.rows.snapshot()
	if status != "failed" || code != codeDuration {
		t.Fatalf("status %q code %q", status, code)
	}
	if len(r.voice.lines) != 4 || r.box.count("node render.mjs") != 0 {
		t.Errorf("voiced %d lines, rendered %d", len(r.voice.lines), r.box.count("node render.mjs"))
	}
	shorten := r.script.Requests()[1].Messages
	if !strings.Contains(shorten[0].Content, "Shorten the narration") || !strings.Contains(shorten[1].Content, `"target_words":`) {
		t.Errorf("the shortening was not asked for a target")
	}
}

// A cancel during the render stops it within a status interval, removes the
// sandbox and finishes as cancelled; the video is never kept.
func TestACancelDuringTheRenderIsNotOverwritten(t *testing.T) {
	r := newRig(t, draftReply("One.", "Two."))
	rendering := make(chan struct{})
	r.box.render = func(ctx context.Context) {
		close(rendering)
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
	}
	go func() {
		<-rendering
		r.rows.setStatus("cancelling")
	}()
	started := time.Now()
	if err := r.job.Run(context.Background(), message()); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("the cancel took %s to stop the render", took)
	}
	status, code, artifact := r.rows.snapshot()
	if status != "cancelled" || code != "cancelled" || artifact != nil {
		t.Fatalf("status %q code %q artifact %v", status, code, artifact)
	}
	if len(r.storedObjects(t)) != 0 || len(r.box.terminated) != 1 {
		t.Errorf("stored %v, sandboxes removed %v", r.storedObjects(t), r.box.terminated)
	}
}

// A cancel that lands between the render and the save is seen by the save
// itself, and the files it had stored are removed.
func TestACancelDuringTheSaveKeepsNothing(t *testing.T) {
	r := newRig(t, draftReply("One.", "Two."))
	r.rows.onPhase = func(phase string) {
		if phase == "saving" {
			r.rows.setStatus("cancelling")
		}
	}
	// The watch must not see it first, so the save's own check is tested.
	r.job.Timing.StatusInterval = time.Hour
	if err := r.job.Run(context.Background(), message()); err != nil {
		t.Fatal(err)
	}
	if status, _, artifact := r.rows.snapshot(); status != "cancelled" || artifact != nil {
		t.Fatalf("status %q artifact %v", status, artifact)
	}
	if objects := r.storedObjects(t); len(objects) != 0 {
		t.Errorf("a cancelled video left %v", objects)
	}
}

// A worker that is stopping gives the attempt back and asks for the
// message to be put back.
func TestAStoppingWorkerGivesTheAttemptBack(t *testing.T) {
	r := newRig(t, draftReply("One.", "Two."))
	ctx, cancel := context.WithCancel(context.Background())
	r.box.render = func(context.Context) { cancel() }
	err := r.job.Run(ctx, message())
	if !errors.Is(err, broker.ErrRequeue) {
		t.Fatalf("err = %v", err)
	}
	if status, _, _ := r.rows.snapshot(); status != "queued" {
		t.Errorf("status %q", status)
	}
}

// The bytes stored must be the bytes verified.
func TestAVideoChangedAfterItsCheckIsNotKept(t *testing.T) {
	r := newRig(t, draftReply("One.", "Two."))
	r.rows.onPhase = func(phase string) {
		if phase == "saving" {
			r.box.put("/workspace/deliverable-job-7-attempt-1.mp4", []byte("something else"))
		}
	}
	if err := r.job.Run(context.Background(), message()); err == nil {
		t.Fatal("a changed video was kept")
	}
	if _, code, _ := r.rows.snapshot(); code != codeVerification {
		t.Errorf("code %q", code)
	}
	if len(r.storedObjects(t)) != 0 {
		t.Errorf("left %v", r.storedObjects(t))
	}
}

// A request this worker was not written for fails at once, before any
// model or sandbox is paid for; a message of another attempt is ignored.
func TestARequestIsCheckedBeforeAnythingIsSpent(t *testing.T) {
	r := newRig(t)
	r.rows.job.Request = []byte(`{"version":1,"brief":"x","root_thread_id":3,"revision_artifact_id":9}`)
	if err := r.job.Run(context.Background(), message()); err == nil {
		t.Fatal("a revision was accepted")
	}
	if status, code, _ := r.rows.snapshot(); status != "failed" || code != codeGeneration || r.box.count("") != 0 {
		t.Errorf("status %q code %q, %d commands", status, code, r.box.count(""))
	}

	other := newRig(t)
	stale := message()
	stale.ID = "deliverable-job:7:attempt:0"
	if err := other.job.Run(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := other.rows.snapshot(); status != "queued" {
		t.Errorf("an old attempt's message changed the row to %q", status)
	}
	if _, err := jobOf(&celery.Message{Args: []any{float64(7)}}); err == nil {
		t.Error("a positional job id was accepted")
	}
}

// A scene that drew nothing is named to the repair, from a frame in the
// middle of that scene, not from wherever the middle of the video fell.
func TestABlankSceneIsNamedToTheRepair(t *testing.T) {
	r := newRig(t, draftReply("One two three four five.", "Six seven eight nine ten."), repairReply(2), repairReply(2))
	r.box.blankScene = true
	if err := r.job.Run(context.Background(), message()); err == nil {
		t.Fatal("a video with a blank scene was kept")
	}
	requests := r.script.Requests()
	if len(requests) != 3 || !strings.Contains(requests[1].Messages[1].Content, "Scene 2's frame at 3.00s is blank") {
		t.Errorf("the repair was told: %s", requests[1].Messages[1].Content[:200])
	}
	// Two seconds of narration each: the middles of 60-frame scenes.
	if samples := frameSamples([]voiced{{Number: 1, Seconds: 2}, {Number: 2, Seconds: 2}}); samples[0].At != 1 || samples[1].At != 3 {
		t.Errorf("samples %+v", samples)
	}
}
