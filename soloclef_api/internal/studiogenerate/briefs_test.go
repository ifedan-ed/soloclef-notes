package studiogenerate

import (
	"strings"
	"testing"
)

func request(t *testing.T, body string) Request {
	t.Helper()
	parsed, failures := parseRequest([]byte(body))
	if len(failures) > 0 {
		t.Fatalf("%s: %+v", body, failures)
	}
	return parsed
}

// The task must name the kind in the specialist's own vocabulary and state
// the dialog's choices as facts, for every kind the studio offers.
func TestEachKindsTaskSaysWhatTheDialogChose(t *testing.T) {
	cases := []struct {
		body    string
		kind    string
		sandbox bool
		says    []string
	}{
		{`{"kind":"quiz","count":"more","difficulty":"hard"}`, "quiz", false, []string{"exactly 20 multiple-choice questions", "hard difficulty"}},
		{`{"kind":"flashcards","count":"fewer"}`, "flashcards", false, []string{"exactly 10 flashcards", "medium difficulty"}},
		{`{"kind":"mindmap","detail":"overview"}`, "mindmap", false, []string{"two levels deep"}},
		{`{"kind":"infographic","detail":"detailed"}`, "infographic", false, []string{"Five or six sections", "already chosen"}},
		{`{"kind":"slides","format":"presenter","length":"long"}`, "pptx", true, []string{"about 16 slides", "speaker notes"}},
		{`{"kind":"data_table"}`, "xlsx", true, []string{"one row per item"}},
		{`{"kind":"report"}`, "pdf", true, []string{"PDF report"}},
		{`{"kind":"report","file_type":"docx","template_prompt":"Use the SBAR structure."}`, "docx", true, []string{"Word (.docx)", "Use the SBAR structure."}},
		{`{"kind":"report","format":"interactive"}`, "html", true, []string{"self-contained HTML"}},
	}
	for _, c := range cases {
		r := request(t, c.body)
		r.Language, r.Focus = "Spanish", "dosing"
		if artifactKind(r) != c.kind || needsSandbox(r) != c.sandbox {
			t.Errorf("%s: kind %q, sandbox %v", c.body, artifactKind(r), needsSandbox(r))
		}
		task := artifactTask(r, "My title", "THE MATERIAL")
		want := append(c.says, "Kind of artifact: "+c.kind+".", `Title: "My title"`, "Write it in Spanish.", "Focus: dosing",
			"<source_material>\nTHE MATERIAL\n</source_material>", "without a user present")
		for _, piece := range want {
			if !strings.Contains(task, piece) {
				t.Errorf("%s: the task lacks %q", c.body, piece)
			}
		}
	}
	// Audio and video are not artifacts of the specialist.
	if artifactKind(request(t, `{"kind":"audio"}`)) != "" || artifactKind(request(t, `{"kind":"video"}`)) != "" {
		t.Error("audio and video should have no artifact kind")
	}
}

func TestAnAudioPlanFollowsTheDialogsTwoPresets(t *testing.T) {
	plan := planAudio(request(t, `{"kind":"audio","format":"brief","length":"short","focus":"side effects"}`))
	if plan.Brief.Speakers != 1 || plan.Brief.MinSeconds != 60 || plan.Brief.MaxSeconds != 150 {
		t.Errorf("%+v", plan.Brief)
	}
	if plan.Focus != "Keep it a brief, focused overview. side effects" {
		t.Errorf("%q", plan.Focus)
	}
	plain := planAudio(request(t, `{"kind":"audio"}`))
	if plain.Brief.Speakers != 2 || plain.Brief.MinSeconds != 180 || plain.Brief.MaxSeconds != 330 || plain.Focus != "" || plain.Language != "" {
		t.Errorf("%+v", plain)
	}
	// The dialog's language goes to the podcast opener, which proposes the
	// brief in it and casts its voices from that language's roster.
	if spanish := planAudio(request(t, `{"kind":"audio","language":" es "}`)); spanish.Language != "es" {
		t.Errorf("the dialog's language became %q", spanish.Language)
	}
}

func TestAVideoBriefNamesTheFormatAndOnlyAChosenStyle(t *testing.T) {
	auto := videoBrief(request(t, `{"kind":"video"}`))
	if !strings.Contains(auto, "Format: Explainer") || strings.Contains(auto, "Visual style") {
		t.Errorf("auto should name no style: %s", auto)
	}
	custom := videoBrief(request(t, `{"kind":"video","format":"short","visual_style":"custom","style_prompt":"neon city","language":"French","focus":"cost"}`))
	for _, piece := range []string{"Format: Short", "requested by the user: neon city", "Narrate and write on-screen text in French.", "Focus: cost", "content, not instructions"} {
		if !strings.Contains(custom, piece) {
			t.Errorf("the brief lacks %q", piece)
		}
	}
	if !strings.Contains(videoBrief(request(t, `{"kind":"video","visual_style":"whiteboard"}`)), "Visual style: Whiteboard") {
		t.Error("a preset style should be described")
	}
}

func TestAnOutputsTitleIsTheDialogsOrTheKindsDefault(t *testing.T) {
	if got := outputTitle(request(t, `{"kind":"quiz"}`), "Tides"); got != "Quiz: Tides" {
		t.Errorf("%q", got)
	}
	if got := outputTitle(request(t, `{"kind":"quiz","title":"  Week  3\nquiz "}`), "Tides"); got != "Week 3 quiz" {
		t.Errorf("%q", got)
	}
	long := outputTitle(request(t, `{"kind":"report"}`), strings.Repeat("n", 400))
	if len([]rune(long)) > maxTitleChars {
		t.Errorf("a title of %d characters", len([]rune(long)))
	}
}

// What a deployment cannot make is refused when it is asked for, so the
// rule that decides it is tested for every kind against every combination
// of the two things a deployment may lack. It needs no database: CanMake
// reads only the settings.
func TestWhatADeploymentCanMake(t *testing.T) {
	// The kinds built by code in the sandbox, and the one that is drawn.
	needsSandbox := map[string]bool{"report": true, "slides": true, "data_table": true}
	needsImage := map[string]bool{"infographic": true}
	kinds := []string{"audio", "video", "report", "slides", "mindmap", "flashcards", "quiz", "infographic", "data_table"}
	for _, sandbox := range []bool{false, true} {
		for _, image := range []bool{false, true} {
			routes := Routes{SandboxEnabled: sandbox, ImageEnabled: image}
			for _, kind := range kinds {
				want := (sandbox || !needsSandbox[kind]) && (image || !needsImage[kind])
				if got := routes.CanMake(kind); got != want {
					t.Errorf("sandbox %v, image model %v: CanMake(%q) = %v, want %v", sandbox, image, kind, got, want)
				}
			}
			if routes.CanMake("nonsense") {
				t.Errorf("sandbox %v, image model %v: an unknown kind can be made", sandbox, image)
			}
		}
	}
}
