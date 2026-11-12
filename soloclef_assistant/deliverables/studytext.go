package deliverables

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// This file is the text rules flashcards and quizzes share. Both are "study
// text": plain text in which the only formatting is LaTeX between \( \) or
// \[ \]. The web viewers of both parse every field with one parser
// (rendering/formats/study-text/parse-text.ts) and refuse to open a file in
// which any field fails it, so a field is checked here against that same
// grammar before anything is stored.

// byteOrderMark is U+FEFF, written as a number because the character itself
// may not appear in Go source.
const byteOrderMark = rune(0xFEFF)

// badCharacter names the first character of text that an artifact may not
// hold, or "". Line breaks and tabs are text; every other control character,
// and the byte-order mark, is something a model emitted by accident and a
// viewer would render as a box.
func badCharacter(text string) string {
	// One pass over the bytes settles nearly every text: it is clean if it
	// has no ASCII control character and nothing past ASCII. Only a text
	// that fails that is decoded rune by rune to say what is in it.
	clean := true
	for i := 0; i < len(text) && clean; i++ {
		c := text[i]
		clean = c < utf8.RuneSelf && c != 0x7f && (c >= 0x20 || c == '\n' || c == '\t')
	}
	if clean {
		return ""
	}
	for _, r := range text {
		switch {
		case r == '\n' || r == '\t':
		case r == byteOrderMark:
			return "a byte-order mark"
		case r == unicode.ReplacementChar:
			return "text that is not valid UTF-8"
		case unicode.IsControl(r):
			return fmt.Sprintf("a control character (U+%04X)", r)
		}
	}
	return ""
}

// checkStudyText appends what is wrong with one field to problems. where
// says which card or question ("card 3"), name which field.
func checkStudyText(problems *[]string, where, name, text string, limit int, required bool) {
	switch {
	case text == "" && required:
		*problems = append(*problems, where+": "+name+" is empty")
	case len(text) > limit && utf8.RuneCountInString(text) > limit:
		// The byte length is asked first because it is free and a text
		// within the limit in bytes is within it in characters.
		*problems = append(*problems, fmt.Sprintf("%s: %s is longer than %d characters", where, name, limit))
	}
	if bad := badCharacter(text); bad != "" {
		*problems = append(*problems, where+": "+name+" contains "+bad)
	}
	if bad := latexProblem(text); bad != "" {
		*problems = append(*problems, where+": "+name+" has "+bad)
	}
}

// latexProblem says what is wrong with the LaTeX delimiters of text, or "".
//
// The grammar is the viewer's: \( opens inline mathematics and \) closes it,
// \[ and \] the same for a displayed formula; they do not nest, an
// expression is not empty, and its braces balance. A delimiter after an odd
// number of backslashes is escaped and is text ("\\(" is a backslash and a
// parenthesis). The viewer answers only "invalid"; each case is named here
// because the reader is a model that has to repair the one field.
//
// It walks bytes: every character it looks for is ASCII, and an ASCII byte
// never occurs inside a longer UTF-8 sequence.
func latexProblem(text string) string {
	// Nearly every field has no mathematics at all; one scan says so.
	if !strings.Contains(text, `\`) {
		return ""
	}
	for i := 0; i < len(text); {
		kind := delimiterAt(text, i)
		if kind == 0 {
			i++
			continue
		}
		if kind == ')' || kind == ']' {
			return `a closing LaTeX delimiter \` + string(kind) + ` with nothing open`
		}
		closing := byte(')')
		if kind == '[' {
			closing = ']'
		}
		start := i + 2
		closed := false
		for i = start; i < len(text); i++ {
			inner := delimiterAt(text, i)
			if inner == 0 {
				continue
			}
			if inner == '(' || inner == '[' {
				return "a LaTeX delimiter opened inside another"
			}
			if inner != closing {
				return `LaTeX opened with \` + string(kind) + ` and closed with \` + string(inner)
			}
			expression := text[start:i]
			if strings.TrimSpace(expression) == "" {
				return "an empty LaTeX expression"
			}
			if !bracesBalance(expression) {
				return "unbalanced braces in a LaTeX expression"
			}
			i += 2
			closed = true
			break
		}
		if !closed {
			return `a LaTeX delimiter \` + string(kind) + ` that is never closed`
		}
	}
	return ""
}

// delimiterAt is the second byte of an unescaped LaTeX delimiter starting
// at i, one of ( ) [ ], or 0 when there is none there.
func delimiterAt(text string, i int) byte {
	if text[i] != '\\' || i+1 >= len(text) {
		return 0
	}
	switch text[i+1] {
	case '(', ')', '[', ']':
		if escaped(text, i) {
			return 0
		}
		return text[i+1]
	}
	return 0
}

// escaped reports whether the byte at i follows an odd number of
// backslashes, which makes it literal.
func escaped(text string, i int) bool {
	backslashes := 0
	for j := i - 1; j >= 0 && text[j] == '\\'; j-- {
		backslashes++
	}
	return backslashes%2 == 1
}

// bracesBalance reports whether every unescaped { of a LaTeX expression has
// its }, in order.
func bracesBalance(expression string) bool {
	depth := 0
	for i := 0; i < len(expression); i++ {
		switch expression[i] {
		case '{':
			if !escaped(expression, i) {
				depth++
			}
		case '}':
			if !escaped(expression, i) {
				depth--
			}
		}
		if depth < 0 {
			return false
		}
	}
	return depth == 0
}

// sameness turns a text into the key two texts share when a reader would
// call them the same: compatibility-normalised, case-folded, and with every
// run of white space made one space. It is how the viewers decide that two
// cards have one front, two questions one text and two options one answer,
// so "Résumé" typed two ways, in two cases or with a doubled space is still
// one.
type sameness struct{ fold cases.Caser }

func newSameness() sameness { return sameness{fold: cases.Fold()} }

func (s sameness) key(text string) string {
	// Most text is ASCII, for which normalising and folding are lower-casing
	// and nothing else. The general path costs about ten times as much, and
	// a quiz asks for a hundred and fifty keys.
	if key, ascii := asciiKey(text); ascii {
		return key
	}
	return strings.Join(strings.Fields(s.fold.String(norm.NFKC.String(text))), " ")
}

// asciiKey is key for a text of ASCII characters only, and false for any
// other text.
func asciiKey(text string) (string, bool) {
	out := make([]byte, 0, len(text))
	space := false
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c >= utf8.RuneSelf:
			return "", false
		case c == ' ' || (c >= '\t' && c <= '\r'):
			// The white space strings.Fields splits ASCII on.
			space = len(out) > 0
			continue
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		}
		if space {
			out = append(out, ' ')
			space = false
		}
		out = append(out, c)
	}
	return string(out), true
}

// checkTitle appends what is wrong with an artifact's title to problems and
// returns the title as it will be stored.
func checkTitle(problems *[]string, title string) string {
	title = strings.TrimSpace(title)
	switch {
	case title == "":
		*problems = append(*problems, "the title is empty")
	case utf8.RuneCountInString(title) > maxTitleChars:
		*problems = append(*problems, fmt.Sprintf("the title is longer than %d characters", maxTitleChars))
	case strings.ContainsAny(title, "\r\n"):
		*problems = append(*problems, "the title must be one line")
	}
	if bad := badCharacter(title); bad != "" {
		*problems = append(*problems, "the title contains "+bad)
	}
	return title
}

// lineEndings makes every line ending the one kind. Built once: building a
// replacer costs more than using it on a page of text.
var lineEndings = strings.NewReplacer("\r\n", "\n", "\r", "\n")

// maxTitleChars is the longest title any artifact may have: the limit the
// viewers of all three formats enforce.
const maxTitleChars = 200

// shownProblems is how many problems one refusal lists. A deck with a
// hundred bad cards would be a hundred lines; the first few tell the model
// what kind of mistake it is making.
const shownProblems = 12

// refusal is the error for something that was not made: what did not happen
// ("the deck was not saved"), then every problem found, in one sentence the
// model can act on.
func refusal(outcome string, problems []string) error {
	if len(problems) > shownProblems {
		problems = append(problems[:shownProblems:shownProblems], fmt.Sprintf("and %d more problems", len(problems)-shownProblems))
	}
	return fmt.Errorf("%s: %s", outcome, strings.Join(problems, "; "))
}
