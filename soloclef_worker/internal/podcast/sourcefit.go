package podcast

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// The source as each drafting call sees it: whole when it fits, otherwise
// condensed by section (app/deliverables/video/source_budget.py, which the
// podcast shares with the video). Every section keeps its heading, its
// opening text, its list items and its tables, and longer passages are
// shortened, rather than the tail being cut off: a transcript that saw only
// the first third of a paper filled the rest from the model's own
// knowledge.

// sourceBudgetRunes is how much source one call is given: 30,000 tokens at
// a conservative three characters a token. The source travels with the
// outline call and with every segment call, so it is capped lower than a
// video's. The Python tier also lowered it for a model with a window under
// 75,000 tokens; every chat model this deployment offers has a larger one.
const sourceBudgetRunes = 30_000 * 3

// condensedNote tells the model the material was shortened.
const condensedNote = "The source material was condensed to fit: every section's heading is kept " +
	"with its opening text, list items and tables, and longer passages are " +
	"shortened (marked […]). Cover the sections from what is shown; do not " +
	"fill the gaps with outside knowledge."

// sourceBlock is the source as a drafting call's user message.
func sourceBlock(source string) string {
	fitted, condensed := fitSource(source, sourceBudgetRunes)
	block := "<source_content>" + fitted + "</source_content>"
	if condensed {
		block += "\n\n" + condensedNote
	}
	return block
}

const ellipsis = " […]"

// caps is the longest text kept per block, tried from generous to
// headings-only: first with every paragraph of a section, then with only its
// opening one.
var caps = []int{2400, 1600, 1000, 700, 450, 300, 200, 120, 60, 0}

type attempt struct {
	cap            int
	everyParagraph bool
}

var attempts = func() []attempt {
	var all []attempt
	for _, c := range caps[:6] {
		all = append(all, attempt{c, true})
	}
	for _, c := range caps {
		all = append(all, attempt{c, false})
	}
	return all
}()

var (
	headingLine = regexp.MustCompile(`^\s{0,3}#{1,6}\s+\S`)
	runIn       = regexp.MustCompile(`(?s)^\s*(\*\*[^*\n](?:[^*]|\*[^*])*?\*\*)(.*)$`)
	listItem    = regexp.MustCompile(`^\s*(?:[-*+•]|\d{1,3}[.)])\s+\S`)
	tableRow    = regexp.MustCompile(`^\s*\|`)
	blankLines  = regexp.MustCompile(`\n\s*\n`)
)

// fitSource is the whole text when it fits, otherwise a condensation.
// Lengths are in runes, as Python's are in code points.
func fitSource(text string, budget int) (string, bool) {
	text = strings.TrimSpace(text)
	if utf8.RuneCountInString(text) <= budget {
		return text, false
	}
	sections := toSections(text)
	for _, try := range attempts {
		condensed := render(sections, try.cap, try.everyParagraph)
		if utf8.RuneCountInString(condensed) <= budget {
			return condensed, true
		}
	}
	// Even the headings alone overflow: keep as many as fit.
	return clipText(render(sections, 0, false), budget-utf8.RuneCountInString(ellipsis)), true
}

type block struct {
	kind  string // heading, run_in, table, item, paragraph
	value string
	lead  string // run_in only
}

type section struct {
	heading string
	lead    string
	blocks  []block
}

func toSections(text string) []section {
	sections := []section{{}}
	for _, b := range toBlocks(text) {
		switch b.kind {
		case "heading":
			sections = append(sections, section{heading: b.value})
		case "run_in":
			sections = append(sections, section{heading: b.value, lead: b.lead})
		default:
			last := &sections[len(sections)-1]
			last.blocks = append(last.blocks, b)
		}
	}
	kept := sections[:0]
	for _, s := range sections {
		if s.heading != "" || s.lead != "" || len(s.blocks) > 0 {
			kept = append(kept, s)
		}
	}
	return kept
}

func toBlocks(text string) []block {
	var blocks []block
	for _, raw := range blankLines.Split(text, -1) {
		var lines []string
		for _, line := range strings.Split(strings.Trim(raw, "\n"), "\n") {
			line = strings.TrimRight(line, " \t\r")
			if strings.TrimSpace(line) != "" {
				lines = append(lines, line)
			}
		}
		if len(lines) == 0 {
			continue
		}
		if headingLine.MatchString(lines[0]) {
			blocks = append(blocks, block{kind: "heading", value: strings.TrimSpace(lines[0])})
			lines = lines[1:]
			if len(lines) == 0 {
				continue
			}
		}
		allTable := true
		for _, line := range lines {
			if !tableRow.MatchString(line) {
				allTable = false
				break
			}
		}
		if allTable {
			blocks = append(blocks, block{kind: "table", value: strings.Join(lines, "\n")})
			continue
		}
		if listItem.MatchString(lines[0]) {
			blocks = append(blocks, listItems(lines)...)
			continue
		}
		trimmed := make([]string, len(lines))
		for index, line := range lines {
			trimmed[index] = strings.TrimSpace(line)
		}
		paragraph := strings.Join(trimmed, " ")
		if match := runIn.FindStringSubmatch(paragraph); match != nil && utf8.RuneCountInString(match[1]) <= 400 {
			blocks = append(blocks, block{kind: "run_in", value: match[1], lead: strings.TrimSpace(match[2])})
		} else {
			blocks = append(blocks, block{kind: "paragraph", value: paragraph})
		}
	}
	return blocks
}

func listItems(lines []string) []block {
	var (
		items []block
		item  []string
	)
	for _, line := range lines {
		if listItem.MatchString(line) && len(item) > 0 {
			items = append(items, block{kind: "item", value: strings.Join(item, " ")})
			item = nil
		}
		item = append(item, strings.TrimSpace(line))
	}
	if len(item) > 0 {
		items = append(items, block{kind: "item", value: strings.Join(item, " ")})
	}
	return items
}

func render(sections []section, cap int, everyParagraph bool) string {
	var out []string
	for _, s := range sections {
		var parts []string
		opening, rest := s.lead, s.blocks
		if opening == "" && len(rest) > 0 && rest[0].kind == "paragraph" {
			opening, rest = rest[0].value, rest[1:]
		}
		if s.heading != "" {
			parts = append(parts, s.heading)
			if opening != "" && cap > 0 {
				// A run-in heading ("**Point 3:** text") keeps its text
				// inline.
				glue := "\n\n"
				if s.lead != "" {
					glue = " "
				}
				parts[len(parts)-1] += glue + clipText(opening, cap)
			}
		} else if opening != "" && cap > 0 {
			parts = append(parts, clipText(opening, cap))
		}
		if cap > 0 {
			parts = append(parts, body(rest, cap, everyParagraph)...)
		}
		if len(parts) > 0 {
			out = append(out, strings.Join(parts, "\n\n"))
		}
	}
	return strings.Join(out, "\n\n")
}

func body(blocks []block, cap int, everyParagraph bool) []string {
	var (
		out   []string
		items []string
	)
	for _, b := range blocks {
		if b.kind == "item" {
			items = append(items, clipText(b.value, max(cap/3, 60)))
			continue
		}
		if len(items) > 0 {
			out = append(out, strings.Join(items, "\n"))
			items = nil
		}
		if b.kind == "table" {
			out = append(out, clipTable(b.value, cap*2))
		} else if everyParagraph {
			out = append(out, clipText(b.value, cap))
		}
	}
	if len(items) > 0 {
		out = append(out, strings.Join(items, "\n"))
	}
	return out
}

// clipText shortens to cap runes, at the end of a sentence when one ends in
// the second half, else at a word.
func clipText(text string, cap int) string {
	runes := []rune(text)
	if len(runes) <= cap {
		return text
	}
	if cap <= 0 {
		return ""
	}
	cut := string(runes[:cap])
	sentence := max(strings.LastIndex(cut, ". "), strings.LastIndex(cut, "? "), strings.LastIndex(cut, "! "))
	if sentence >= 0 && utf8.RuneCountInString(cut[:sentence]) >= cap/2 {
		return cut[:sentence+1] + ellipsis
	}
	if space := strings.LastIndex(cut, " "); space > 0 {
		cut = cut[:space]
	}
	return strings.TrimRight(cut, " ") + ellipsis
}

func clipTable(table string, cap int) string {
	if utf8.RuneCountInString(table) <= cap {
		return table
	}
	var (
		kept []string
		size int
	)
	for _, row := range strings.Split(table, "\n") {
		length := utf8.RuneCountInString(row)
		if len(kept) > 0 && size+length+1 > cap {
			kept = append(kept, "| […] |")
			break
		}
		kept = append(kept, row)
		size += length + 1
	}
	return strings.Join(kept, "\n")
}
