package deckspec

import (
	"fmt"
	"strconv"
	"strings"
)

// Markdown is the deck as text: the artifact's document, which search
// indexes and a screen reader is given. Every word on the slides is in it,
// with the speaker notes, in reading order; narration too for a video,
// since that is most of what a video says.
func Markdown(deck Deck) string {
	var b strings.Builder
	b.WriteString("# " + deck.Title + "\n")
	if deck.Subtitle != "" {
		b.WriteString("\n" + deck.Subtitle + "\n")
	}
	for i, s := range deck.Slides {
		heading := s.Title
		if heading == "" {
			heading = "Quote"
		}
		fmt.Fprintf(&b, "\n## %d. %s\n\n", i+1, heading)
		for _, line := range slideLines(s) {
			b.WriteString(line + "\n")
		}
		if len(s.Cites) > 0 {
			cites := make([]string, len(s.Cites))
			for j, c := range s.Cites {
				cites[j] = strconv.Itoa(c)
			}
			b.WriteString("\nSources: " + strings.Join(cites, ", ") + "\n")
		}
		if s.Notes != "" {
			b.WriteString("\nNotes: " + strings.ReplaceAll(s.Notes, "\n", " ") + "\n")
		}
		if len(s.Narration) > 0 {
			b.WriteString("\nNarration: " + Spoken(s) + "\n")
		}
	}
	if len(deck.Sources) > 0 {
		b.WriteString("\n## Sources\n\n")
		for i, source := range deck.Sources {
			line := fmt.Sprintf("%d. %s", i+1, source.Title)
			if source.Detail != "" {
				line += ". " + source.Detail
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}

// slideLines is a slide's content, less its title, as Markdown lines.
func slideLines(s Slide) []string {
	var out []string
	switch s.Kind {
	case KindTitle, KindSection:
		if s.Subtitle != "" {
			out = append(out, s.Subtitle)
		}
	case KindBullets:
		for _, b := range s.Bullets {
			out = append(out, "- "+b)
		}
	case KindTwoColumn:
		for _, c := range []*Column{s.Left, s.Right} {
			out = append(out, "### "+c.Heading)
			for _, b := range c.Bullets {
				out = append(out, "- "+b)
			}
		}
	case KindComparison:
		out = append(out, "| | "+strings.Join(s.Columns, " | ")+" |", "|---"+strings.Repeat("|---", len(s.Columns))+"|")
		for _, r := range s.Rows {
			out = append(out, "| "+r.Aspect+" | "+strings.Join(r.Cells, " | ")+" |")
		}
	case KindQuote:
		out = append(out, "> "+s.Quote)
		if s.Attribution != "" {
			out = append(out, ">", "> — "+s.Attribution)
		}
	case KindTimeline:
		for _, e := range s.Events {
			out = append(out, "- "+e.Label+": "+e.Text)
		}
	case KindStats:
		for _, st := range s.Stats {
			out = append(out, "- "+st.Value+": "+st.Label)
		}
	case KindDiagram:
		for i, st := range s.Steps {
			line := fmt.Sprintf("%d. %s", i+1, st.Label)
			if st.Text != "" {
				line += ": " + st.Text
			}
			out = append(out, line)
		}
		if s.Style == DiagramCycle {
			out = append(out, "", "(The last step leads back to the first.)")
		}
	case KindChart:
		for _, bar := range s.Bars {
			out = append(out, "- "+bar.Label+": "+FormatNumber(bar.Value)+unitSuffix(s.Unit))
		}
		if s.Caption != "" {
			out = append(out, "", s.Caption)
		}
	}
	return out
}

func unitSuffix(unit string) string {
	switch {
	case unit == "":
		return ""
	case unit == "%":
		return "%"
	}
	return " " + unit
}

// FormatNumber writes a chart's value as a reader expects it: no trailing
// zeros, thousands grouped, at most two decimals.
func FormatNumber(v float64) string {
	s := strconv.FormatFloat(v, 'f', 2, 64)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	whole, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if frac != "" {
		b.WriteString("." + frac)
	}
	return b.String()
}
