package posterrender

import (
	"encoding/json"
	"strings"
)

// Markdown is the poster as text: what the account's search indexes, what
// a screen reader is given, and what the model is shown of it later. A
// figure is its title and its alt text, which say what it shows.
func Markdown(p Poster) string {
	var b strings.Builder
	b.WriteString("# " + p.Title + "\n")
	if p.Subtitle != "" {
		b.WriteString("\n" + p.Subtitle + "\n")
	}
	if len(p.Numbers) > 0 {
		b.WriteString("\n")
		for _, n := range p.Numbers {
			b.WriteString("- **" + n.Value + "** " + n.Label + "\n")
		}
	}
	for _, s := range p.Sections {
		b.WriteString("\n## " + s.Heading + "\n\n")
		for _, point := range s.Points {
			b.WriteString("- " + point + "\n")
		}
		if title, alt := figureWords(s.Figure); title != "" {
			if len(s.Points) > 0 {
				b.WriteString("\n")
			}
			b.WriteString("Figure: " + title + ". " + alt + "\n")
		}
	}
	if p.Takeaway != "" {
		b.WriteString("\n" + p.Takeaway + "\n")
	}
	if p.Sources != "" {
		b.WriteString("\nSources: " + p.Sources + "\n")
	}
	return b.String()
}

// figureWords is a checked figure's title and alt text.
func figureWords(spec json.RawMessage) (title, alt string) {
	if len(spec) == 0 {
		return "", ""
	}
	var f struct {
		Title string `json:"title"`
		Alt   string `json:"alt"`
	}
	_ = json.Unmarshal(spec, &f)
	return f.Title, f.Alt
}

// Content is the poster as kept with the artifact: the checked content
// (its figures as checked specs), from which it can be drawn again in
// another style or orientation, or by a newer layout.
func Content(p Poster) ([]byte, error) {
	return json.Marshal(struct {
		Title string `json:"title"`
		Poster
	}{p.Title, p})
}
