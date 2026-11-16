package artifacts

import (
	"strings"
	"unicode"
)

// # The one rule a download's name follows
//
// Downloads were named three ways: a long report title cut at 80 characters
// in the middle of a phrase, with its colon turned into " _ " ("Walkthrough
// C _ Calcium ... Prevention in.pdf"); short titles as they were
// ("Walkthrough C.docx"); and a poster's SVG by the slug it was stored
// under ("walkthrough-c.svg") beside a PNG named by its title. Now every
// file is the title, cleaned and cut the same way:
//
//   - letters and digits of any script, spaces, hyphens and underscores are
//     kept; an apostrophe is dropped ("Children's" -> "Childrens"); every
//     other character (a path separator, a wildcard, a colon, a dot) is a
//     space, and runs of space are one;
//   - a name longer than nameLimit is cut at the last clause boundary (a
//     colon, a dash, a comma ...) that keeps at least half of it, otherwise
//     at the last word that fits, and only a single word longer than the
//     whole limit is cut inside it.
const (
	// nameLimit is the most characters (not bytes) a name keeps before its
	// extension: long enough for any listing title, short enough to read
	// in a file manager's column.
	nameLimit = 60
	nameless  = "artifact"
)

// clauseMarks end a clause inside a title: the places a cut reads as the
// end of something rather than the middle of a phrase.
const clauseMarks = ":;,.!?()[]—–|"

// fileStem is a title as a filename, without the extension.
func fileStem(title string) string {
	var cleaned strings.Builder
	// cuts are the positions (in runes of cleaned) where a clause ended.
	var cuts []int
	written, pendingSpace := 0, false
	for _, character := range strings.TrimSpace(title) {
		switch {
		case character == '\'' || character == '’':
			continue
		case unicode.IsLetter(character) || unicode.IsNumber(character) || character == '-' || character == '_':
			if pendingSpace && written > 0 {
				cleaned.WriteByte(' ')
				written++
			}
			pendingSpace = false
			cleaned.WriteRune(character)
			written++
		default:
			if strings.ContainsRune(clauseMarks, character) {
				cuts = append(cuts, written)
			}
			pendingSpace = true
		}
	}
	name := []rune(cleaned.String())
	if len(name) == 0 {
		return nameless
	}
	if len(name) <= nameLimit {
		return string(name)
	}
	cut := 0
	for _, at := range cuts {
		if at <= nameLimit && at >= nameLimit/2 {
			cut = at
		}
	}
	if cut == 0 {
		for at := nameLimit; at > 0; at-- {
			if name[at] == ' ' {
				cut = at
				break
			}
		}
	}
	if cut == 0 {
		cut = nameLimit
	}
	return strings.TrimRight(string(name[:cut]), " -_")
}
