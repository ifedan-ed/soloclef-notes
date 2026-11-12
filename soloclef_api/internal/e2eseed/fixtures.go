package e2eseed

import (
	"embed"
	"encoding/json"
	"fmt"
)

// The files a seeded output is made of. They are real files of each kind
// (a two-page PDF, a Word document, a deck, a workbook of two sheets, a
// picture, three seconds of tone, two seconds of video), made once and
// kept here: small, fixed, and the same on every run. Compiled in because
// the routes are part of the one binary; they are a hundred or so
// kilobytes and nothing reads them unless the routes are switched on.
//
//go:embed fixtures
var fixtures embed.FS

func fixture(name string) []byte {
	data, err := fixtures.ReadFile("fixtures/" + name)
	if err != nil {
		// A name spelled wrongly in this package; there is no input that
		// reaches here.
		panic("e2eseed: " + err.Error())
	}
	return data
}

// seedFile is one file of a seeded artifact.
type seedFile struct {
	role, filename, mimeType string
	data                     []byte
}

// seeded is what a kind is made of: the studio's own kind, the artifact's
// format, its text, and its files (none for a mind map, which is its
// Markdown).
type seeded struct {
	studioKind, format, markdown string
	files                        []seedFile
}

const (
	pdfMime  = "application/pdf"
	jsonMime = "application/json"
)

// mindMapMarkdown is a mind map as the viewer draws one: headings and
// their points.
const mindMapMarkdown = `# E2E mind map
## Photosynthesis
- Light reactions
- Calvin cycle
## Respiration
- Glycolysis
- Krebs cycle
`

// filesFor is the seed for one of the kinds the suite asks for.
func filesFor(kind, title string) (seeded, error) {
	primary := func(filename, mimeType string, data []byte) seedFile {
		return seedFile{role: "primary", filename: filename, mimeType: mimeType, data: data}
	}
	preview := func(name string) seedFile {
		return seedFile{role: "preview", filename: name, mimeType: pdfMime, data: fixture(name)}
	}
	switch kind {
	case "audio":
		return seeded{"audio", "podcast",
			"# " + title + "\n\n**Speaker 0:** Welcome to the E2E overview.\n\n**Speaker 1:** Thanks, glad to be here.",
			[]seedFile{primary("overview.mp3", "audio/mpeg", fixture("overview.mp3"))}}, nil
	case "video":
		return seeded{"video", "video", "# " + title + "\n\nE2E video.",
			[]seedFile{primary("overview.mp4", "video/mp4", fixture("overview.mp4"))}}, nil
	case "report":
		return seeded{"report", "pdf", "# " + title + "\n\nE2E report.",
			[]seedFile{primary("report.pdf", pdfMime, fixture("report.pdf"))}}, nil
	case "report_docx":
		return seeded{"report", "docx", "# " + title + "\n\nE2E Word report.", []seedFile{
			primary("report.docx", "application/vnd.openxmlformats-officedocument.wordprocessingml.document", fixture("report.docx")),
			preview("report-preview.pdf")}}, nil
	case "report_html":
		page := "<!doctype html><html><body><h1>E2E interactive report</h1><p>Seeded.</p></body></html>"
		return seeded{"report", "html", "# " + title + "\n\nE2E interactive report.",
			[]seedFile{primary("report.html", "text/html", []byte(page))}}, nil
	case "slides":
		return seeded{"slides", "pptx", "# " + title + "\n\nE2E slides.", []seedFile{
			primary("deck.pptx", "application/vnd.openxmlformats-officedocument.presentationml.presentation", fixture("deck.pptx")),
			preview("deck-preview.pdf")}}, nil
	case "mindmap":
		return seeded{"mindmap", "mindmap", mindMapMarkdown, nil}, nil
	case "flashcards":
		return seeded{"flashcards", "flashcards", "# " + title + "\n\nE2E flashcards.",
			[]seedFile{primary("flashcards.json", jsonMime, flashcards(title))}}, nil
	case "quiz":
		return seeded{"quiz", "quiz", "# " + title + "\n\nE2E quiz.",
			[]seedFile{primary("quiz.json", jsonMime, quiz(title))}}, nil
	case "data_table":
		return seeded{"data_table", "xlsx", "# " + title + "\n\nE2E data table.", []seedFile{
			primary("table.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", fixture("table.xlsx"))}}, nil
	case "infographic":
		return seeded{"infographic", "infographic", "# " + title + "\n\nE2E infographic.",
			[]seedFile{primary("infographic.png", "image/png", fixture("infographic.png"))}}, nil
	}
	return seeded{}, errInvalid(fmt.Sprintf("unknown kind %q", kind))
}

// flashcards is a deck of six, the first with a hint, in the file format
// the study routes read.
func flashcards(title string) []byte {
	cards := make([]map[string]string, 0, 6)
	for i := 1; i <= 6; i++ {
		cards = append(cards, map[string]string{
			"front_text": fmt.Sprintf("E2E question %d", i), "back_text": fmt.Sprintf("E2E answer %d", i),
		})
	}
	cards[0]["hint_text"] = "E2E hint"
	return mustJSON(map[string]any{"schema_version": 1, "title": title, "cards": cards})
}

// quiz is five questions whose right answer is always the second option.
// The first three explain every option; the last two have the one
// explanation an older quiz had, so the viewer is tried on both.
func quiz(title string) []byte {
	questions := make([]map[string]any, 0, 5)
	for i := 1; i <= 5; i++ {
		question := map[string]any{
			"question_text":        fmt.Sprintf("E2E quiz question %d?", i),
			"options":              []string{fmt.Sprintf("Option %dA", i), fmt.Sprintf("Option %dB", i), fmt.Sprintf("Option %dC", i), fmt.Sprintf("Option %dD", i)},
			"correct_option_index": 1,
			"explanation_text":     fmt.Sprintf("Option %dB is right because the seed says so.", i),
		}
		if i <= 3 {
			question["option_rationales"] = []string{
				fmt.Sprintf("Rationale %dA: not this one.", i), fmt.Sprintf("Rationale %dB: this is the one.", i),
				fmt.Sprintf("Rationale %dC: not this one either.", i), fmt.Sprintf("Rationale %dD: a common mix-up.", i),
			}
		}
		questions = append(questions, question)
	}
	return mustJSON(map[string]any{"schema_version": 1, "title": title, "questions": questions})
}

func mustJSON(value any) []byte {
	data, err := json.Marshal(value)
	if err != nil {
		panic("e2eseed: " + err.Error())
	}
	return data
}
