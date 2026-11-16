package videojob

import (
	"errors"
	"math"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// A slide is held for its narration with the lead-in and the pause, on
// whole frames, each part coming in a little before its passage's measured
// start, however unevenly the passages ran; and only the moments in which
// something moves are drawn.
func TestTheFilmIsPlannedToTheNarration(t *testing.T) {
	video, err := readDeck([]byte(deckJSON(firstSlide, secondSlide)))
	if err != nil {
		t.Fatal(err)
	}
	// Measured, not even: a short opening, then a passage of figures said
	// in words that ran long.
	voices := []voiced{
		{Number: 1, Seconds: 6, Starts: []float64{0, 1.1, 4.7}},
		{Number: 2, Seconds: 4, Starts: []float64{0, 2.9, 3.2}},
	}
	shots, err := plan(video, voices)
	if err != nil {
		t.Fatal(err)
	}
	// 0.4 + 6 + 0.7 seconds is 213 frames; the second starts after it.
	if shots[0].Frames != 213 || shots[1].Start != 213 || shots[1].Frames != 153 {
		t.Errorf("shots %+v", shots)
	}
	// The heading at once; each part as its passage begins, a quarter
	// second early.
	for i, voice := range voices {
		for k := range voice.Starts {
			want := 0.0
			if k > 0 {
				want = leadIn + voice.Starts[k] - earlyBy
			}
			if d := shots[i].Starts[k] - want; d > 1e-9 || d < -1e-9 {
				t.Errorf("slide %d step %d starts at %.3f, want %.3f", i+1, k, shots[i].Starts[k], want)
			}
		}
	}
	// A narration that does not have one passage per step is a fault
	// here, said, not spread out.
	if _, err := plan(video, []voiced{voices[0], {Number: 2, Seconds: 4, Starts: []float64{0}}}); err == nil ||
		!strings.Contains(err.Error(), "slide 2 has 3 build steps and 1 voiced passages") {
		t.Errorf("a slide short of passages: %v", err)
	}
	film := cuts(video, shots)
	held, total := 0, 0
	for _, c := range film {
		total += c.Frames
		if c.Frames > 1 {
			held++
		}
	}
	if total != totalFrames(shots) || len(film) > total/2 || held == 0 {
		t.Errorf("%d pictures for %d frames, %d held", len(film), total, held)
	}
	// The second slide fades in over the first: its first frames say so.
	for _, c := range film {
		if c.Picture.Slide == 1 && c.Picture.At == 0 && c.Picture.Fade <= 0 {
			t.Errorf("the second slide's first frame does not fade: %+v", c)
		}
	}
	// Every slide ends still: its last picture is held to its end.
	samples := frameSamples(shots)
	if samples[0].At != 0.4+6 || samples[1].Slide != 2 {
		t.Errorf("samples %+v", samples)
	}
}

// A passage starts after every passage before it and a gap after each:
// the times audiofile.JoinSpaced lays them at.
func TestPassagesStartAfterTheOnesBefore(t *testing.T) {
	starts := passageStarts([]float64{1.5, 2.25, 0.8}, 0.2)
	for k, want := range []float64{0, 1.7, 4.15} {
		if math.Abs(starts[k]-want) > 1e-9 {
			t.Errorf("passage %d starts at %.3f, want %.3f", k+1, starts[k], want)
		}
	}
	if starts := passageStarts([]float64{3}, 0.2); len(starts) != 1 || starts[0] != 0 {
		t.Errorf("one passage starts at %v", starts)
	}
}

// thread and oversize text are refused; white space is normalised.
func TestTheRequestIsReadStrictly(t *testing.T) {
	request, err := readRequest([]byte(`{"version":1,"brief":"  The   heart\n","source_references":[" a  b "],"root_thread_id":3,"source_material":" text ","max_seconds":90}`))
	if err != nil || request.Brief != "The heart" || request.SourceReferences[0] != "a b" || request.SourceMaterial != "text" || request.limitSeconds() != 90 {
		t.Errorf("read %+v (%v)", request, err)
	}
	if request, _ := readRequest([]byte(`{"version":1,"brief":"x","root_thread_id":3,"max_seconds":900}`)); request.limitSeconds() != 600 {
		t.Errorf("a cap above the service's limit raised it to %d", request.limitSeconds())
	}
	for name, column := range map[string]string{
		"unknown field":  `{"version":1,"brief":"x","root_thread_id":3,"style":"fun"}`,
		"version":        `{"version":2,"brief":"x","root_thread_id":3}`,
		"no thread":      `{"version":1,"brief":"x"}`,
		"empty brief":    `{"version":1,"brief":"   ","root_thread_id":3}`,
		"revision":       `{"version":1,"brief":"x","root_thread_id":3,"revision_artifact_id":4}`,
		"repeated":       `{"version":1,"brief":"x","root_thread_id":3,"source_references":["a","a"]}`,
		"long brief":     `{"version":1,"brief":"` + strings.Repeat("x", maxBriefRunes+1) + `","root_thread_id":3}`,
		"string version": `{"version":"1","brief":"x","root_thread_id":3}`,
	} {
		if _, err := readRequest([]byte(column)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

// A deck is read, checked and laid out in one go, and what is wrong with
// it is said in the words a model can act on.
func TestADeckIsReadCheckedAndLaidOut(t *testing.T) {
	video, err := readDeck([]byte(deckJSON(firstSlide, secondSlide)))
	if err != nil || len(video.Scenes) != 2 || video.Deck.Theme != "classic" || video.spoken()[0] != strings.Join(firstSlide, " ") {
		t.Fatalf("video %+v (%v)", video.Deck, err)
	}
	for name, test := range map[string]struct{ reply, says string }{
		"no narration":    {strings.Replace(deckJSON(firstSlide, secondSlide), `"narration":["It beats all your life.","About seventy-two times a minute at rest.","Moving five litres every minute."]`, `"notes":"It beats."`, 1), "slide 2 (stats) has 0 narration passages and 3 parts"},
		"a passage over":  {deckJSON(append(append([]string{}, firstSlide...), "And more."), secondSlide), "slide 1 (bullets) has 4 narration passages and 3 parts; give exactly 3 narration passages, one as it appears and then one for each of its 2 bullets"},
		"a passage short": {deckJSON(firstSlide, secondSlide[:2]), "slide 2 (stats) has 2 narration passages and 3 parts; give exactly 3 narration passages, one as it appears and then one for each of its 2 figures"},
		"sources":         {strings.Replace(deckJSON(firstSlide, secondSlide), `"theme":"classic"`, `"theme":"classic","sources":[{"title":"A book"}]`, 1), "a video's deck lists no sources"},
	} {
		if _, err := readDeck([]byte(test.reply)); err == nil || !strings.Contains(err.Error(), test.says) {
			t.Errorf("%s: %v", name, err)
		}
	}
	prompt := authorPrompt(90)
	if !strings.Contains(prompt, "2 to 20 slides") || !strings.Contains(prompt, "under 178 words") || !strings.Contains(prompt, "narration is a list of passages") ||
		!strings.Contains(prompt, "exactly one passage for the slide and one for each part") || !strings.Contains(prompt, "Write exactly one passage per part") {
		t.Errorf("author prompt:\n%s", prompt)
	}
	// A shortened narration keeps each slide's passages.
	if _, err := readShortened(video.Deck)([]byte(`{"slides":[{"narration":["One."]},{"narration":["Two.","Three.","Four."]}]}`)); err == nil || !strings.Contains(err.Error(), "slide 1 has 1 passages and needs 3") {
		t.Errorf("a shortening that dropped passages: %v", err)
	}
}

// The failure codes come from the stage, and the row's diagnostic carries
// no credential.
func TestFailuresAreCodedByStageAndScrubbed(t *testing.T) {
	if code := codeOf(fail(codeRender, errors.New("the model said 'verify'"))); code != codeRender {
		t.Errorf("code %s", code)
	}
	if code := codeOf(fail(codeRender, fail(codeVerification, errors.New("x")))); code != codeVerification {
		t.Errorf("the inner stage's code was lost: %s", code)
	}
	if code := codeOf(fail(codeGeneration, &model.Error{Status: 402, Message: "out of credit"})); code != codeQuota {
		t.Errorf("a 402 is %s", code)
	}
	if fail(codeRender, errCancelled) != errCancelled {
		t.Error("a cancel became a failure")
	}
	text := diagnostic(errors.New("calling https://user:pw@proxy/v1 with api_key=sk-123 and Bearer abc\nfailed " + strings.Repeat("é", 1500)))
	if strings.Contains(text, "pw@") || strings.Contains(text, "sk-123") || strings.Contains(text, "abc") || strings.Contains(text, "\n") || len(text) > maxDiagnostic {
		t.Errorf("diagnostic %q", text[:200])
	}
}

// The download's name is the title, safe for a file.
func TestNames(t *testing.T) {
	for title, want := range map[string]string{
		"The heart: a tour?": "The heart a tour.mp4",
		"Ελληνικά / 101":     "Ελληνικά 101.mp4",
		"???":                "video-7.mp4",
		" .. ":               "video-7.mp4",
	} {
		if got := videoFilename(title, 7); got != want {
			t.Errorf("%q = %q, want %q", title, got, want)
		}
	}
}

// Every setting is required once the video is on, the voice must be the
// speech service's, and "off" must be said.
func TestTheSettings(t *testing.T) {
	service := speech.Service{Roster: speech.OpenAI, Model: "gpt-4o-mini-tts"}
	if _, err := LoadConfig(func(string) string { return "" }, service); err == nil {
		t.Error("an unset switch was read as off")
	}
	if config, err := LoadConfig(func(name string) string { return map[string]string{Enabled: "false"}[name] }, service); err != nil || config.Enabled {
		t.Errorf("off = %+v (%v)", config, err)
	}
	values := map[string]string{
		Enabled: "true", Resvg: "/usr/local/bin/resvg", FrameWorkers: "6", RenderTimeout: "20m", MaxRenders: "1",
		NarrationVoice: "openai:marin",
	}
	config, err := LoadConfig(func(name string) string { return values[name] }, service)
	if err != nil || config.Resvg != "/usr/local/bin/resvg" || config.FrameWorkers != 6 || config.Voice != "marin" || config.RenderTimeout.Minutes() != 20 {
		t.Fatalf("config %+v (%v)", config, err)
	}
	values[NarrationVoice] = "kokoro:af_heart"
	values[FrameWorkers] = "none"
	delete(values, Resvg)
	_, err = LoadConfig(func(name string) string { return values[name] }, service)
	for _, named := range []string{NarrationVoice, FrameWorkers, Resvg} {
		if err == nil || !strings.Contains(err.Error(), named) {
			t.Errorf("%s was not reported: %v", named, err)
		}
	}
}
