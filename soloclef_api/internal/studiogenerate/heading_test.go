package studiogenerate

import (
	"strings"
	"testing"
)

// An output is headed by its subject and never by the kind's name, whatever
// language the dialog's default title is in; a title that is not the
// dialog's default is the subject as given.
func TestAnOutputIsHeadedByItsSubjectNotItsKind(t *testing.T) {
	cases := []struct {
		name, body, notebook, want string
	}{
		{"the live defect", `{"kind":"infographic","title":"Infographic: Apollo 11: the first Moon landing"}`, "Apollo 11: the first Moon landing", "Apollo 11: the first Moon landing"},
		{"the web in Spanish", `{"kind":"report","title":"Informe: Mareas"}`, "Mareas", "Mareas"},
		{"the web in Chinese", `{"kind":"slides","title":"演示文稿: 潮汐"}`, "潮汐", "潮汐"},
		{"an installed phone's English", `{"kind":"quiz","title":"Quiz: Tides"}`, "Tides", "Tides"},
		{"spacing the dialog did not mean", `{"kind":"quiz","title":"Quiz:  Spring\ntides "}`, " Spring tides", "Spring tides"},
		{"no title sent", `{"kind":"mindmap"}`, "Tides", "Tides"},
		{"a report suggestion's own title", `{"kind":"quiz","title":"Test the oxygen thresholds"}`, "Bronchiolitis", "Test the oxygen thresholds"},
		{"a title naming another notebook", `{"kind":"quiz","title":"Quiz: Waves"}`, "Tides", "Quiz: Waves"},
		{"the notebook's title alone", `{"kind":"quiz","title":"Tides"}`, "Tides", "Tides"},
		{"a separator with nothing before it", `{"kind":"quiz","title":": Tides"}`, "Tides", ": Tides"},
		{"a title that only begins like the notebook", `{"kind":"quiz","title":"Quiz: Ti"}`, "Tides", "Quiz: Ti"},
	}
	for _, c := range cases {
		if got := outputHeading(request(t, c.body), c.notebook); got.Text != c.want {
			t.Errorf("%s: headed %q, want %q", c.name, got.Text, c.want)
		}
	}
}

// A notebook still called "Untitled notebook", in any of the apps'
// languages, names no subject: the output is not headed with the
// placeholder, and its maker is told to name the subject from the material.
func TestAnUntitledNotebooksOutputNamesItsOwnSubject(t *testing.T) {
	for _, body := range []string{`{"kind":"infographic","title":"Infographic: Untitled notebook"}`, `{"kind":"infographic","title":"Infografía: Cuaderno sin título"}`, `{"kind":"infographic"}`} {
		r := request(t, body)
		notebook := "Untitled notebook"
		if strings.Contains(body, "Cuaderno") {
			notebook = "Cuaderno sin título"
		}
		own := outputHeading(r, notebook)
		if own.Text != "" {
			t.Errorf("%s: headed %q", body, own.Text)
		}
		task := artifactTask(r, own, "THE MATERIAL")
		if !strings.Contains(task, "Title: none is chosen yet") || strings.Contains(task, notebook) {
			t.Errorf("%s: the task's title line: %s", body, task)
		}
	}

	video := request(t, `{"kind":"video"}`)
	untitled := outputHeading(video, "Untitled notebook")
	if got := videoTitle(untitled, "Video Overview: Untitled notebook"); got != "Video Overview: Untitled notebook" {
		t.Errorf("an untitled video is queued as %q", got)
	}
	if !strings.Contains(videoBrief(video, untitled), untitledVideo) {
		t.Error("an untitled video's brief should say its title is not its subject")
	}
	named := outputHeading(video, "Tides")
	if got := videoTitle(named, "Video Overview: Tides"); got != "Tides" || strings.Contains(videoBrief(video, named), untitledVideo) {
		t.Errorf("a named video is queued as %q", got)
	}
}

// A notebook titled past about 185 characters has its default title cut;
// the cut title is still the untouched default and the heading is the
// whole notebook title, with words and in Chinese.
func TestACutDefaultTitleIsHeadedByTheWholeNotebookTitle(t *testing.T) {
	notebooks := map[string]string{
		"English": strings.Repeat("The Moon and the tides: ", 8) + "and more", // 200 characters, the most a notebook title holds
		"Chinese": strings.Repeat("阿波罗登月计划", 27) + "：首次载人登月任务",                // 198 characters
	}
	for language, notebook := range notebooks {
		for _, label := range []string{"Infographic", "信息图"} {
			// What the web and the phones send, as the server cuts it.
			sent := label + ": " + notebook
			r := request(t, `{"kind":"infographic","title":"`+sent+`"}`)
			if r.Title == sent {
				t.Fatalf("%s: the test's title was not long enough to be cut", language)
			}
			if got := outputHeading(r, notebook); got.Text != notebook {
				t.Errorf("%s, %s: headed %q", language, label, got.Text)
			}
		}
	}
	// A cut that is not the cut of this notebook's default is a title of
	// its own.
	notebook := notebooks["English"]
	other := request(t, `{"kind":"quiz","title":"Quiz: The Moon and the tides"}`)
	if got := outputHeading(other, notebook); got.Text != "Quiz: The Moon and the tides" {
		t.Errorf("a short title beginning like a long notebook was headed %q", got.Text)
	}
}
