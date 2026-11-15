package videojob

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

func message() *celery.Message {
	return &celery.Message{Task: Task, ID: "deliverable-job:7:attempt:1", Kwargs: map[string]any{"job_id": float64(7)}}
}

// Two slides: a heading and two bullets, a heading and two figures, each
// part with its own passage.
var (
	firstSlide  = []string{"The heart has four chambers.", "The two atria receive blood.", "The two ventricles pump it out."}
	secondSlide = []string{"It beats all your life.", "About seventy-two times a minute at rest.", "Moving five litres every minute."}
)

// A video goes from the request to a kept artifact: the MP4 as its primary
// file, each slide's narration stored, and the slides the video route
// serves; one model call, and only the moving frames drawn.
func TestAVideoIsMadeAndKeptInTheShapesItsReadersRead(t *testing.T) {
	r := newRig(t, deckReply(firstSlide, secondSlide))
	if err := r.job.Run(context.Background(), message()); err != nil {
		t.Fatal(err)
	}
	status, _, artifact := r.rows.snapshot()
	if status != "ready" || artifact == nil {
		t.Fatalf("status %q, artifact %v", status, artifact)
	}
	if got := r.notified.reported(); len(got) != 1 || got[0] != (notify.StudioRun{UserID: r.rows.job.UserID, JobID: r.rows.job.ID}) {
		t.Errorf("notified %+v", got)
	}
	if artifact.Format != "video" || artifact.Primary.MimeType != "video/mp4" || artifact.Primary.Filename != "The heart.mp4" ||
		!strings.HasPrefix(artifact.Primary.Key, "artifacts/"+r.rows.job.UserID+"/55/primary/") {
		t.Errorf("primary file %+v", artifact.Primary)
	}
	var meta struct {
		Verification map[string]any   `json:"verification"`
		Duration     float64          `json:"duration_seconds"`
		Theme        string           `json:"theme"`
		Slides       []map[string]any `json:"slides"`
		SceneCodes   []map[string]any `json:"scene_codes"`
	}
	if err := json.Unmarshal(artifact.Metadata, &meta); err != nil {
		t.Fatal(err)
	}
	// 16 and 17 words at two and a half a second, the passage gaps
	// between each slide's three passages, with the lead-in and the
	// pause: 7.5 and 7.9 seconds and the gaps.
	if want := 15.4 + 4*passageGap; meta.Verification["verified"] != true || math.Abs(meta.Duration-want) > 0.2 || meta.Theme != "classic" {
		t.Errorf("metadata %s", artifact.Metadata)
	}
	if len(meta.Slides) != 2 || meta.SceneCodes == nil || len(meta.SceneCodes) != 0 {
		t.Fatalf("slides %d, scene codes %v", len(meta.Slides), meta.SceneCodes)
	}
	first := meta.Slides[0]
	key, _ := first["audio_storage_key"].(string)
	if first["slide_number"] != float64(1) || first["title"] != "The heart has four chambers" || first["storage_backend"] != "local" ||
		!strings.HasPrefix(key, "artifacts/"+r.rows.job.UserID+"/55/slides/slide-1-") || math.Abs(first["duration_in_frames"].(float64)-math.Ceil((7.5+2*passageGap)*fps)) > 1 ||
		!strings.Contains(first["content_in_markdown"].(string), "- **Atria:** receive blood") {
		t.Errorf("first slide %v", first)
	}
	objects := r.storedObjects(t)
	if len(objects) != 3 {
		t.Errorf("stored %v, want the video and two narrations", objects)
	}
	if !strings.Contains(artifact.Markdown, "# The heart") || !strings.Contains(artifact.Markdown, "Narration: The heart has four chambers. The two atria receive blood.") {
		t.Errorf("markdown %q", artifact.Markdown)
	}
	// Every passage is voiced on its own, in the configured voice, and
	// each slide's passages are joined with the pause between them.
	spoken := map[string]bool{}
	for _, line := range r.voice.lines {
		spoken[line.Text] = line.Voice == "marin" && line.Instructions == "unhurried"
	}
	for _, passage := range append(append([]string{}, firstSlide...), secondSlide...) {
		if !spoken[passage] {
			t.Errorf("passage %q was not voiced alone in the configured voice", passage)
		}
	}
	if len(r.voice.lines) != 6 || len(r.voice.joins) != 2 || r.voice.joins[0] != (fakeJoin{parts: 3, gap: passageGap}) {
		t.Errorf("voiced %d lines, joined %+v", len(r.voice.lines), r.voice.joins)
	}
	// The stored transcript is the slide's passages as one text, and the
	// measured start of each passage is kept with it: 5 and 5 words at
	// two and a half a second, the passage gap after each.
	if first["speaker_transcripts"].([]any)[0] != strings.Join(firstSlide, " ") {
		t.Errorf("transcript %v", first["speaker_transcripts"])
	}
	starts, _ := first["passage_starts_seconds"].([]any)
	for k, want := range []float64{0, 2 + passageGap, 2 * (2 + passageGap)} {
		if len(starts) != 3 || math.Abs(starts[k].(float64)-want) > 0.0005 {
			t.Errorf("passage starts %v, want passage %d at %.3f", starts, k+1, want)
		}
	}
	// The author's deck is the only model call; and of 463 frames, only
	// the ones in which something moves are drawn.
	if len(r.script.Requests()) != 1 || r.raster.count() == 0 || r.raster.count() > 200 {
		t.Errorf("model calls %d, frames drawn %d", len(r.script.Requests()), r.raster.count())
	}
	var names []string
	for _, stage := range r.stages {
		names = append(names, stage.Name)
	}
	if got := strings.Join(names, ","); got != "authoring,narration,frames,encode,verify,save" {
		t.Errorf("stages measured: %s", got)
	}
}

// A deck the model wrote wrongly is sent back with every problem, once
// for each of its deckFixes rounds; one still wrong after them fails the
// video before anything is voiced.
func TestADeckWrittenWronglyIsSentBackThenRefused(t *testing.T) {
	wrong := strings.Replace(deckReply(firstSlide, secondSlide), `"classic"`, `"neon"`, 1)
	replies := make([]string, deckFixes+1)
	for i := range replies {
		replies[i] = wrong
	}
	r := newRig(t, replies...)
	if err := r.job.Run(context.Background(), message()); err == nil {
		t.Fatal("a deck with an unknown theme was made")
	}
	if _, code, _ := r.rows.snapshot(); code != codeGeneration {
		t.Errorf("code %q", code)
	}
	requests := r.script.Requests()
	if len(requests) != deckFixes+1 {
		t.Fatalf("%d model calls, want the deck and %d corrections", len(requests), deckFixes)
	}
	for _, request := range requests[1:] {
		if last := request.Messages[len(request.Messages)-1].Content; !strings.Contains(last, `theme "neon" is not a theme`) {
			t.Errorf("the correction: %s", last)
		}
	}
	if len(r.voice.lines) != 0 {
		t.Errorf("voiced %d lines", len(r.voice.lines))
	}
}

// Narration measured over the limit is shortened once and voiced again; a
// second overrun is duration_limit, before anything is drawn.
func TestOverlongNarrationIsShortenedOnceThenRefused(t *testing.T) {
	long := strings.TrimSpace(strings.Repeat("word ", 90))
	passages := []string{long, long, long}
	shortened := `{"slides":[{"narration":["` + long + `","` + long + `","` + long + `"]},{"narration":["` + long + `","` + long + `","` + long + `"]}]}`
	r := newRig(t, deckReply(passages, passages), shortened)
	// Slow enough that 540 words overrun 600 seconds; and the word budget
	// (1,296) does not catch them before voicing.
	r.voice.wordsPerSecond = 0.5
	if err := r.job.Run(context.Background(), message()); err == nil {
		t.Fatal("an overlong video was made")
	}
	status, code, _ := r.rows.snapshot()
	if status != "failed" || code != codeDuration {
		t.Fatalf("status %q code %q", status, code)
	}
	// Three passages a slide, two slides, voiced twice.
	if len(r.voice.lines) != 12 || r.raster.count() != 0 {
		t.Errorf("voiced %d lines, drew %d frames", len(r.voice.lines), r.raster.count())
	}
	shorten := r.script.Requests()[1].Messages
	if !strings.Contains(shorten[0].Content, "Shorten the narration") || !strings.Contains(shorten[1].Content, `"target_words":`) {
		t.Errorf("the shortening was not asked for a target")
	}
}

// A cancel while the frames are drawn stops them within a status interval
// and finishes as cancelled; the video is never kept.
func TestACancelDuringTheRenderIsNotOverwritten(t *testing.T) {
	r := newRig(t, deckReply(firstSlide, secondSlide))
	var once sync.Once
	r.raster.render = func(ctx context.Context) {
		once.Do(func() { r.rows.setStatus("cancelling") })
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
	}
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
	if got := r.notified.reported(); len(got) != 0 {
		t.Errorf("a cancel notified %+v", got)
	}
	if len(r.storedObjects(t)) != 0 {
		t.Errorf("stored %v", r.storedObjects(t))
	}
}

// A cancel that lands between the render and the save is seen by the save
// itself, and the files it had stored are removed.
func TestACancelDuringTheSaveKeepsNothing(t *testing.T) {
	r := newRig(t, deckReply(firstSlide, secondSlide))
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
	r := newRig(t, deckReply(firstSlide, secondSlide))
	ctx, cancel := context.WithCancel(context.Background())
	r.raster.render = func(context.Context) { cancel() }
	err := r.job.Run(ctx, message())
	if !errors.Is(err, broker.ErrRequeue) {
		t.Fatalf("err = %v", err)
	}
	if status, _, _ := r.rows.snapshot(); status != "queued" {
		t.Errorf("status %q", status)
	}
}

// A request this worker was not written for fails at once, before any
// model is paid for; a message of another attempt is ignored.
func TestARequestIsCheckedBeforeAnythingIsSpent(t *testing.T) {
	r := newRig(t)
	r.rows.job.Request = []byte(`{"version":1,"brief":"x","root_thread_id":3,"revision_artifact_id":9}`)
	if err := r.job.Run(context.Background(), message()); err == nil {
		t.Fatal("a revision was accepted")
	}
	if status, code, _ := r.rows.snapshot(); status != "failed" || code != codeGeneration || len(r.script.Requests()) != 0 {
		t.Errorf("status %q code %q, %d model calls", status, code, len(r.script.Requests()))
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

// A blank picture is found, slide by slide, and the video is not kept.
func TestABlankSlideFailsVerification(t *testing.T) {
	r := newRig(t, deckReply(firstSlide, secondSlide))
	r.raster.png = blankPNG(t)
	if err := r.job.Run(context.Background(), message()); err == nil {
		t.Fatal("a blank video was kept")
	}
	_, code, _ := r.rows.snapshot()
	if code != codeVerification || !strings.Contains(r.rows.internal, "Slide 1's frame at") {
		t.Errorf("code %q: %s", code, r.rows.internal)
	}
}

// TestARealVideoToLookAt draws a four-slide video with the real resvg and
// keeps it, to be sampled into frames and looked at: SOLOCLEF_TEST_RESVG
// names resvg and SOLOCLEF_TEST_KEEP_VIDEO the directory.
func TestARealVideoToLookAt(t *testing.T) {
	binary, dir := os.Getenv(realResvg), os.Getenv(keepVideo)
	if binary == "" || dir == "" {
		t.Skipf("%s and %s are not set", realResvg, keepVideo)
	}
	deck := map[string]any{
		"title": "How the heart pumps", "theme": os.Getenv("SOLOCLEF_TEST_THEME"),
		"slides": []any{
			map[string]any{"kind": "title", "title": "How the heart pumps", "subtitle": "Four chambers, four valves, one direction",
				"narration": []string{"This is how the heart pumps blood around the body.", "Four chambers and four valves keep it moving one way."}},
			map[string]any{"kind": "diagram", "style": "flow", "title": "One beat, step by step", "steps": []any{
				map[string]any{"label": "Atria fill", "text": "Blood returns from the body and the lungs"},
				map[string]any{"label": "Atria squeeze", "text": "Blood is pushed down into the ventricles"},
				map[string]any{"label": "Ventricles squeeze", "text": "Blood leaves for the lungs and the body"}},
				"narration": []string{"One beat happens in three steps.", "First the atria fill with blood returning from the body.", "Then they squeeze it down into the ventricles.", "Then the ventricles push it out to the lungs and the body."}},
			map[string]any{"kind": "stats", "title": "A lifetime of beats", "stats": []any{
				map[string]any{"value": "72", "label": "beats a minute at rest"}, map[string]any{"value": "5 L", "label": "of blood pumped every minute"},
				map[string]any{"value": "2.5 billion", "label": "beats in an average lifetime"}},
				"narration": []string{"The numbers are large.", "About seventy-two beats a minute at rest.", "Five litres of blood every minute.", "Two and a half billion beats in a lifetime."}},
			map[string]any{"kind": "bullets", "title": "Valves keep blood moving one way", "bullets": []string{
				"**Tricuspid and mitral:** between atria and ventricles", "**Pulmonary and aortic:** where blood leaves the heart", "A leaking valve lets blood flow back"},
				"narration": []string{"The valves are what keep the flow going one way.", "Two sit between the atria and the ventricles.", "Two more guard the exits.", "When one leaks, blood flows back, and the heart works harder."}},
		},
	}
	if deck["theme"] == "" {
		deck["theme"] = "classic"
	}
	encoded, _ := json.Marshal(deck)
	r := newRig(t, string(encoded))
	raster, err := deckrender.NewRasteriser(binary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.job.Raster = raster
	started := time.Now()
	if err := r.job.Run(context.Background(), message()); err != nil {
		t.Fatalf("%v: %s", err, r.rows.internal)
	}
	t.Logf("made in %s", time.Since(started).Round(time.Millisecond))
	for _, stage := range r.stages {
		t.Logf("%-10s %s", stage.Name, stage.Took.Round(time.Millisecond))
	}
	_, _, artifact := r.rows.snapshot()
	stored, err := r.files.Open(context.Background(), artifact.Primary.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer stored.Close()
	mp4, _ := io.ReadAll(stored)
	if err := os.WriteFile(filepath.Join(dir, "heart-"+deck["theme"].(string)+".mp4"), mp4, 0o644); err != nil {
		t.Fatal(err)
	}
}
