package deliverables

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// FormatMindMap is a mind map. The string is the one the artifacts table and
// the web viewer registry use.
const FormatMindMap = "mindmap"

// A mind map is stored as Markdown and nothing else: one level-one heading,
// the root, and a nested bullet list under it. The web viewer draws the map
// from that text (rendering/formats/mindmap, which parses the artifact's
// markdown_representation), so the artifact has a document and no file.
//
// The specialist this replaces also rendered a 2400x1600 PNG in a sandbox
// and stored it as the artifact's file. Nothing drew that picture: the
// viewer has always drawn from the Markdown, and its own Download menu
// exports the PNG or SVG in the browser. Not storing it removes a browser
// launch from every mind map and the whole class of "the picture and the
// Markdown disagree" saves.

// The limits of a mind map: a map a person can read on one screen. The
// viewer itself would draw up to a thousand nodes; these are the bounds the
// instructions give the model, and every count includes the root.
const (
	maxMapNodes   = 60
	maxMapDepth   = 6
	maxLabelChars = 120
	// mapIndent is the indentation of one level in the stored Markdown.
	mapIndent = "  "
)

// MindMap is a checked mind map.
type MindMap struct {
	Title string
	// Markdown is the map in its one stored spelling: "# root", a blank
	// line, then "- label" items indented two spaces per level.
	Markdown string
	// Nodes and Depth describe the map, root included.
	Nodes int
	Depth int
}

// linksOrMarkup reports whether a label contains a link or an image
// (inline or by reference: "[" text "]" then "(" or "[") or anything shaped
// like an HTML tag ("<" something ">"). The viewer strips links and escapes
// HTML; they are refused here so that what is stored is what is shown,
// wherever else the Markdown is rendered.
//
// It is two scans rather than a regular expression, which took most of the
// time of checking a map.
func linksOrMarkup(label string) bool {
	for i := 0; i < len(label); i++ {
		switch label[i] {
		case '<':
			// At least one character before the closing bracket: "<>" is
			// two characters of text.
			if end := strings.IndexByte(label[i+1:], '>'); end > 0 {
				return true
			}
		case '[':
			end := strings.IndexByte(label[i+1:], ']')
			if after := i + 1 + end + 1; end >= 0 && after < len(label) && (label[after] == '(' || label[after] == '[') {
				return true
			}
		}
	}
	return false
}

// NewMindMap checks a title and the map's Markdown and returns the map in
// its stored spelling, or every problem found.
//
// What the model writes is read line by line rather than through a Markdown
// parser, because the accepted language is deliberately tiny and a parser
// would accept (and silently reshape) far more: a numbered list, a table, a
// second heading. The bullet may be -, * or +, and a level may be indented
// by two, three or four spaces as long as the map is consistent; the map is
// stored with "-" and two spaces whatever was written, so the viewer is
// never handed an indentation Markdown reads as a code block.
func NewMindMap(title, markdown string) (MindMap, error) {
	var problems []string
	title = checkTitle(&problems, title)

	markdown = lineEndings.Replace(markdown)
	var out strings.Builder
	var (
		rooted   bool // the heading has been read
		unit     int  // spaces per level, set by the first indented item
		previous = -1 // the level of the item before; -1 before the first
		items    int  // list items accepted so far
		depth    = 1  // the root alone is one level
	)
	for number, line := range strings.Split(markdown, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		at := "line " + strconv.Itoa(number+1)
		if bad := badCharacter(strings.ReplaceAll(line, "\t", " ")); bad != "" {
			problems = append(problems, at+" contains "+bad)
			continue
		}
		if !rooted {
			// The first line with anything on it is the root.
			rooted = true
			label, isHeading := strings.CutPrefix(line, "#")
			if !isHeading || label == "" || (label[0] != ' ' && label[0] != '\t') {
				problems = append(problems, at+` must be the root, written "# " and its label`)
				continue
			}
			label = strings.TrimSpace(label)
			if bad := labelProblem(label); bad != "" {
				problems = append(problems, at+": the root "+bad)
				continue
			}
			out.WriteString("# " + label + "\n\n")
			continue
		}

		indent := len(line) - len(strings.TrimLeft(line, " "))
		item := line[indent:]
		if strings.HasPrefix(item, "#") {
			problems = append(problems, at+" is a second heading; a mind map has one, the root, and everything else is a list item")
			continue
		}
		if len(item) < 3 || !strings.ContainsRune("-*+", rune(item[0])) || (item[1] != ' ' && item[1] != '\t') {
			problems = append(problems, at+` must be a list item, written "- " and its label, indented with spaces`)
			continue
		}
		label := strings.TrimSpace(item[2:])
		if bad := labelProblem(label); bad != "" {
			problems = append(problems, at+": the label "+bad)
			continue
		}

		level := 0
		switch {
		case indent == 0:
		case previous < 0:
			problems = append(problems, at+" is indented, but the first item under the root starts at the margin")
			continue
		case unit == 0:
			// The first indented item says how wide a level is.
			if indent < 2 || indent > 4 {
				problems = append(problems, fmt.Sprintf("%s is indented %d spaces; use 2 spaces per level", at, indent))
				continue
			}
			unit, level = indent, 1
		case indent%unit != 0:
			problems = append(problems, fmt.Sprintf("%s is indented %d spaces, which is not a whole number of levels (%d spaces each)", at, indent, unit))
			continue
		default:
			level = indent / unit
		}
		if level > previous+1 {
			problems = append(problems, at+" skips a level: an item is at most one level deeper than the item above it")
			continue
		}
		previous = level
		items++
		// The root is depth 1 and the items at the margin depth 2.
		depth = max(depth, level+2)
		out.WriteString(strings.Repeat(mapIndent, level) + "- " + label + "\n")
	}

	nodes := items + 1
	switch {
	case !rooted:
		problems = append(problems, `the mind map is empty; write "# " and the root's label, then the list`)
	case items == 0 && len(problems) == 0:
		// The viewer refuses a root with no children. Said only when
		// nothing else is wrong: a map whose every item was refused above
		// has items, and the model has already been told about each.
		problems = append(problems, "the root has no items under it")
	}
	if nodes > maxMapNodes {
		problems = append(problems, fmt.Sprintf("a mind map has at most %d nodes including the root; this one has %d. Simplify it or split it into two maps", maxMapNodes, nodes))
	}
	if depth > maxMapDepth {
		problems = append(problems, fmt.Sprintf("a mind map is at most %d levels deep including the root; this one is %d", maxMapDepth, depth))
	}
	if len(problems) > 0 {
		return MindMap{}, refusal("the mind map was not saved", problems)
	}
	return MindMap{Title: title, Markdown: out.String(), Nodes: nodes, Depth: depth}, nil
}

// labelProblem says what is wrong with one node's label, as the end of a
// sentence ("is empty"), or "".
func labelProblem(label string) string {
	switch {
	case label == "":
		return "is empty"
	case utf8.RuneCountInString(label) > maxLabelChars:
		return fmt.Sprintf("is longer than %d characters; a node is a few words, not a sentence", maxLabelChars)
	case strings.Contains(label, "```") || strings.Contains(label, "~~~") || strings.HasPrefix(label, ":::"):
		return "contains a code fence or a directive"
	case linksOrMarkup(label):
		return "contains a link, an image or HTML; write the words only"
	case startsBlock(label):
		// "- 1. Steps" is a numbered list inside the item and "- > note" a
		// quotation: Markdown would draw a different tree from the one
		// written.
		return "begins with Markdown structure (a heading, a quotation, a list or a table); write the words only"
	}
	// "**" and "``" are labels with no text in them once drawn, and the
	// viewer refuses a node with nothing to show.
	for _, r := range label {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return ""
		}
	}
	return "has no letters or digits"
}

// startsBlock reports whether a label opens with something Markdown reads
// as block structure rather than words.
func startsBlock(label string) bool {
	switch label[0] {
	case '#', '>', '|':
		return true
	case '-', '*', '+':
		// "- x" is a nested list; "*emphasis*" and "-5 degrees" are words.
		return len(label) > 1 && (label[1] == ' ' || label[1] == '\t')
	}
	// "1. x" and "1) x" start a numbered list.
	digits := 0
	for digits < len(label) && label[digits] >= '0' && label[digits] <= '9' {
		digits++
	}
	return digits > 0 && digits <= 9 && digits+1 < len(label) &&
		(label[digits] == '.' || label[digits] == ')') && (label[digits+1] == ' ' || label[digits+1] == '\t')
}
