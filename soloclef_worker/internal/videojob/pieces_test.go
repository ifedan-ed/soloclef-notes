package videojob

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/speech"
)

// The layout report is read strictly: one entry per scene, numbered as the
// props number them, or the check did not run.
func TestTheLayoutReportIsReadStrictly(t *testing.T) {
	report := `{"scenes":[{"slide_number":1,"findings":[]},{"slide_number":2,"findings":["  text \"x\" is 20 px; nothing may be under 30 px ",""]}]}`
	findings, err := layoutFindings([]byte(report), []int{1, 2})
	if err != nil || len(findings) != 1 || findings[0] != `scene 2: text "x" is 20 px; nothing may be under 30 px` {
		t.Errorf("findings %q (%v)", findings, err)
	}
	long := `{"scenes":[{"slide_number":1,"findings":["` + strings.Repeat("a", 500) + `"]}]}`
	if findings, err := layoutFindings([]byte(long), []int{1}); err != nil || len([]rune(findings[0])) != len("scene 1: ")+maxFindingRunes {
		t.Errorf("a long finding was not cut: %d (%v)", len(findings), err)
	}
	for name, bad := range map[string]string{
		"not json":      "Error: Chrome crashed",
		"a scene short": `{"scenes":[{"slide_number":1,"findings":[]}]}`,
		"misnumbered":   `{"scenes":[{"slide_number":1,"findings":[]},{"slide_number":3,"findings":[]}]}`,
	} {
		if _, err := layoutFindings([]byte(bad), []int{1, 2}); err == nil {
			t.Errorf("%s was read as a report", name)
		}
	}
}

// A scene's frames are counted from the decimal the narration's length
// prints as: 2.1 s at 30 fps is 63 frames, not 64 from a binary rounding.
func TestTheTimelineIsExact(t *testing.T) {
	for _, c := range []struct {
		seconds float64
		want    int
	}{{2.1, 63}, {2.1000001, 64}, {0.2, 30}, {10, 300}, {4.016, 121}} {
		if got, err := sceneFrames(c.seconds, 30, 30); err != nil || got != c.want {
			t.Errorf("%v s = %d frames (%v), want %d", c.seconds, got, err, c.want)
		}
	}
	grid, err := buildTimeline([]voiced{{Number: 1, Seconds: 2.1}, {Number: 2, Seconds: 10}}, 30, 30, entranceFrames(30))
	if err != nil {
		t.Fatal(err)
	}
	if grid.totalFrames() != 363 || grid.Scenes[1].Start != 63 || grid.Scenes[0].Entrance != 63 || grid.Scenes[1].Entrance != 120 {
		t.Errorf("grid %+v", grid)
	}
	if _, err := grid.withRendered(map[int]int{1: 63, 2: 121}); err == nil {
		t.Error("an entrance longer than asked was accepted")
	}
	shorter, err := grid.withRendered(map[int]int{1: 62, 2: 120})
	if err != nil || shorter.Scenes[0].stillFrames() != 1 || shorter.totalFrames() != 363 {
		t.Errorf("a shorter entrance: %+v (%v)", shorter, err)
	}
	plan, err := planAssembly([]scenePart{{1, "/w/e1.mp4", "/w/s1.png", "/w/public/slide-1.mp3"}, {2, "/w/e2.mp4", "/w/s2.png", "/w/public/slide-2.mp3"}}, shorter, "/w", "/w.mp4")
	if err != nil {
		t.Fatal(err)
	}
	joined := fmt.Sprint(plan.Commands)
	if len(plan.Commands) != 5 || !strings.Contains(joined, "-frames:v 1 ") || !strings.Contains(joined, "atrim=end_sample=100800") ||
		!strings.Contains(joined, "atrim=end_sample=480000") || !strings.Contains(plan.Manifest, "hold-2.mp4") {
		t.Errorf("plan %v\n%s", plan.Commands, plan.Manifest)
	}
	if _, err := samplesPerFrame(7); err == nil {
		t.Error("7 fps divides 48 kHz?")
	}
}

// The request column is read strictly: unknown fields, a revision, a missing
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

// A draft is read under the names models use, given its numbers and
// filenames, and held to the harness's twelve scenes.
func TestADraftIsReadAndNumbered(t *testing.T) {
	video, err := readDraft([]byte(`{"language":"en","scenes":[{"narration":"Hi.","onScreenMarkdown":"# A","tsx_module":"x"},{"transcript":"Bye.","on_screen_markdown":"B","code":"y","slide_number":9}]}`))
	if err != nil || len(video.Scenes) != 2 || video.Scenes[1].Number != 2 || video.Scenes[1].Filename != "scene-02.tsx" || video.Scenes[0].Code != "x" {
		t.Fatalf("draft %+v (%v)", video, err)
	}
	thirteen := `{"scenes":[` + strings.TrimSuffix(strings.Repeat(`{"transcript":"a","on_screen_markdown":"b","code":"c"},`, 13), ",") + `]}`
	if _, err := readDraft([]byte(thirteen)); err == nil {
		t.Error("thirteen scenes were accepted")
	}
	if _, err := readDraft([]byte(`{"scenes":[{"transcript":"a","code":"c"}]}`)); err == nil || !strings.Contains(err.Error(), "on_screen_markdown") {
		t.Errorf("a scene without text: %v", err)
	}
	if _, err := readRepair(2)([]byte(`{"scenes":[{"on_screen_markdown":"b","code":"c"}]}`)); err == nil {
		t.Error("a repair that dropped a scene was accepted")
	}
	if !strings.Contains(authorPrompt(90), "1-12 scenes") || !strings.Contains(authorPrompt(90), "under 194 words") || !strings.Contains(authorPrompt(90), staggerImport) {
		t.Errorf("author prompt:\n%s", authorPrompt(90))
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
	if got := slideTitle(Scene{Number: 3, Markdown: "Intro line\n\n## **Valves** ##\n"}); got != "Valves" {
		t.Errorf("title %q", got)
	}
	if got := slideTitle(Scene{Number: 3, Markdown: "  \n"}); got != "Scene 3" {
		t.Errorf("title %q", got)
	}
}

// Every setting is required once the render is on, the voice must be the
// speech service's, and "off" must be said.
func TestTheSettings(t *testing.T) {
	service := speech.Service{Roster: speech.OpenAI, Model: "gpt-4o-mini-tts"}
	if _, err := LoadConfig(func(string) string { return "" }, service); err == nil {
		t.Error("an unset switch was read as off")
	}
	if config, err := LoadConfig(func(name string) string { return map[string]string{Enabled: "false"}[name] }, service); err != nil || config.Enabled {
		t.Errorf("off = %+v (%v)", config, err)
	}
	key := t.TempDir() + "/key"
	if err := writeFile(key, "secret\n"); err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		Enabled: "true", SandboxStack: "examplestack", SandboxURL: "http://sandbox:8080", SandboxKeyFile: key, SandboxImage: "soloclef-sandbox-local:latest",
		SandboxCPU: "6", SandboxMemory: "6Gi", SandboxIdleTTL: "15m", SandboxCommandTimeout: "5m", RenderTimeout: "45m",
		RenderMode: "stills", RenderConcurrency: "6", FramesPerSegment: "1800", FrameTimeout: "7s", MaxRenders: "1",
		NarrationVoice: "openai:marin",
	}
	config, err := LoadConfig(func(name string) string { return values[name] }, service)
	if err != nil || config.Sandbox.APIKey != "secret" || config.Sandbox.Stack != "examplestack" || config.Voice != "marin" || config.Mode != ModeStills || config.RenderTimeout.Minutes() != 45 {
		t.Fatalf("config %+v (%v)", config, err)
	}
	values[NarrationVoice] = "kokoro:af_heart"
	values[RenderMode] = "fast"
	values[FrameTimeout] = "2s"
	delete(values, SandboxStack)
	_, err = LoadConfig(func(name string) string { return values[name] }, service)
	for _, named := range []string{NarrationVoice, RenderMode, FrameTimeout, SandboxStack} {
		if err == nil || !strings.Contains(err.Error(), named) {
			t.Errorf("%s was not reported: %v", named, err)
		}
	}
}
