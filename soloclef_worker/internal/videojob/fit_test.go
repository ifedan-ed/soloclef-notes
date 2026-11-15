package videojob

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_studio/deckrender"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/llm"
)

// clinicalDraft is the renderer's clinical regression deck, narrated, as
// the author's model would reply with it: the deck a video failed on (14
// Nov 2026, job 29) with "a word in the figures' values is too long".
func clinicalDraft(t testing.TB) []byte {
	t.Helper()
	raw, err := os.ReadFile("../../../soloclef_studio/deckrender/testdata/asthma.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// The video path takes the clinical deck as it is: its figures are drawn
// in wider cards, the long names and the address fit, and nothing goes
// back to the model.
func TestAClinicalVideoDeckIsAccepted(t *testing.T) {
	video, err := readDeck(clinicalDraft(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(video.Scenes) != 10 {
		t.Fatalf("%d scenes", len(video.Scenes))
	}
	if _, err := plan(video, voicedEvenly(video, 8)); err != nil {
		t.Fatal(err)
	}
}

// A word no arrangement can hold is sent back naming the slide, the field,
// the word and the room: what the model needs to fix it in one round.
func TestAVideoWordThatCannotFitIsSentBackByName(t *testing.T) {
	var deck map[string]any
	if err := json.Unmarshal(clinicalDraft(t), &deck); err != nil {
		t.Fatal(err)
	}
	deck["slides"].([]any)[1].(map[string]any)["stats"].([]any)[1].(map[string]any)["value"] = "Electroencephalographies"
	raw, _ := json.Marshal(deck)
	_, err := readDeck(raw)
	var rejected *llm.Rejection
	if !errors.As(err, &rejected) {
		t.Fatalf("got %v, want a rejection for the model", err)
	}
	for _, want := range []string{`slide 2 (stats) stat 2's value: the word "Electroencephalographies"`, "the line is 333 pt", "room for about"} {
		if !strings.Contains(rejected.Problems, want) {
			t.Errorf("no %q in %q", want, rejected.Problems)
		}
	}
}

// TestClinicalVideoFrames draws the clinical video's slides as the video
// does, mid-build and settled, for a person to look at
// (SOLOCLEF_TEST_RESVG names resvg, SOLOCLEF_TEST_DECK_OUT the directory).
func TestClinicalVideoFrames(t *testing.T) {
	binary, out := os.Getenv(realResvg), os.Getenv("SOLOCLEF_TEST_DECK_OUT")
	if binary == "" || out == "" {
		t.Skip("SOLOCLEF_TEST_RESVG and SOLOCLEF_TEST_DECK_OUT are not set")
	}
	video, err := readDeck(clinicalDraft(t))
	if err != nil {
		t.Fatal(err)
	}
	drawFrames(t, binary, filepath.Join(out, "video-frames"), video)
}

// drawFrames writes each slide's frame halfway through its build and its
// settled frame, from the shots the film is planned with.
func drawFrames(t *testing.T, binary, dir string, video Authored) {
	t.Helper()
	raster, err := deckrender.NewRasteriser(binary, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	shots, err := plan(video, voicedEvenly(video, 8))
	if err != nil {
		t.Fatal(err)
	}
	for i, scene := range video.Scenes {
		settled := scene.Settled(shots[i].Starts)
		for name, at := range map[string]float64{"building": settled / 2, "settled": settled + 0.5} {
			file := filepath.Join(dir, fmt.Sprintf("slide-%02d-%s.png", i+1, name))
			if err := raster.PNG(context.Background(), scene.Frame(shots[i].Starts, at), file); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// counting is a model that counts its calls: how many rounds an author
// needed.
type counting struct {
	model.Model
	calls int
}

func (c *counting) Generate(ctx context.Context, req model.Request, stream func(model.Chunk)) (model.Response, error) {
	c.calls++
	return c.Model.Generate(ctx, req, stream)
}

// TestAuthorsAClinicalVideoWithALiveModel writes a video's deck from a
// clinical source with a real model through the proxy, as a job does, and
// draws its frames. It runs only when pointed at a proxy:
// SOLOCLEF_TEST_LIVE_LITELLM_URL (ending in /v1), _KEY_FILE, _MODEL, and
// SOLOCLEF_TEST_LIVE_SOURCE, a JSON file of {title, brief,
// source_material}; frames go to SOLOCLEF_TEST_DECK_OUT when resvg is
// named.
func TestAuthorsAClinicalVideoWithALiveModel(t *testing.T) {
	base, keyFile, name, sourceFile := os.Getenv("SOLOCLEF_TEST_LIVE_LITELLM_URL"), os.Getenv("SOLOCLEF_TEST_LIVE_LITELLM_KEY_FILE"),
		os.Getenv("SOLOCLEF_TEST_LIVE_MODEL"), os.Getenv("SOLOCLEF_TEST_LIVE_SOURCE")
	if base == "" || keyFile == "" || name == "" || sourceFile == "" {
		t.Skip("no live proxy named")
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(sourceFile)
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Title, Brief   string
		SourceMaterial string `json:"source_material"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		t.Fatal(err)
	}
	proxy := llm.Proxy{BaseURL: base, Key: strings.TrimSpace(string(key)), HTTP: http.DefaultClient, CallTimeout: 10 * time.Minute,
		Retry: model.RetryPolicy{Attempts: 2, Backoff: 5 * time.Second, MaxBackoff: 10 * time.Second}}
	// As Writers.Client opens the account's studio model: reasoning left on.
	client, err := proxy.Client(name, false)
	if err != nil {
		t.Fatal(err)
	}
	counted := &counting{Model: client}
	started := time.Now()
	video, err := author{model: counted, timeout: 10 * time.Minute}.write(context.Background(), source.Title,
		Request{Brief: source.Brief, SourceMaterial: source.SourceMaterial})
	t.Logf("%d model calls in %s", counted.calls, time.Since(started).Round(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, s := range video.Deck.Slides {
		kinds[string(s.Kind)]++
	}
	t.Logf("%d slides: %v", len(video.Deck.Slides), kinds)
	for i, s := range video.Deck.Slides {
		for _, st := range s.Stats {
			t.Logf("slide %d figure %q: %s", i+1, st.Value, st.Label)
		}
	}
	if binary, out := os.Getenv(realResvg), os.Getenv("SOLOCLEF_TEST_DECK_OUT"); binary != "" && out != "" {
		drawFrames(t, binary, filepath.Join(out, "live-video"), video)
		deck, _ := json.MarshalIndent(video.Deck, "", "  ")
		_ = os.WriteFile(filepath.Join(out, "live-video", "deck.json"), deck, 0o644)
	}
}
