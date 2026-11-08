package mime

import (
	"strings"
	"unicode/utf8"
)

// maxLineLength is email.policy.default's max_line_length.
const maxLineLength = 78

// foldUnstructured writes a header whose value is free text (Subject) the way
// email._header_value_parser._refold_parse_tree writes it.
//
// Two behaviours in it are easy to get wrong and invisible in a happy-path
// test. An encoded word absorbs everything back to the previous encoded word
// on the same line, plain words included, which is why "Über Your automation
// “Weekly" goes out as one encoded word and not two with raw words between
// them. And a fold ends that absorption, so the next non-ASCII word on the new
// line starts a fresh encoded word.
func foldUnstructured(name, value string) string {
	f := folder{line: name + ":", lastEW: -1}
	for _, t := range tokenize(value) {
		f.add(t)
	}
	return f.done()
}

// token is one run of whitespace and the word after it, as the parser's
// terminals come out: the whitespace is kept as it was written, because a fold
// moves it to the front of the next line.
type token struct {
	space string
	word  string
}

func tokenize(value string) []token {
	var tokens []token
	rest := value
	for rest != "" {
		space := leading(rest, true)
		rest = rest[len(space):]
		word := leading(rest, false)
		rest = rest[len(word):]
		if space == "" && word == "" {
			break
		}
		tokens = append(tokens, token{space: space, word: word})
	}
	// "Subject:" and its value are separated by the first whitespace token,
	// and a value that starts with a word has none of its own.
	if len(tokens) > 0 && tokens[0].space == "" {
		tokens[0].space = " "
	}
	return tokens
}

func leading(s string, whitespace bool) string {
	for i := 0; i < len(s); i++ {
		if isWSP(s[i]) != whitespace {
			return s[:i]
		}
	}
	return s
}

func isWSP(c byte) bool {
	return c == ' ' || c == '\t'
}

// needsEncoding is want_encoding for a free-text token: anything that is not
// us-ascii. A line ending never reaches here -- Build refuses the whole
// message for one, as the policy does.
func needsEncoding(word string) bool {
	for i := 0; i < len(word); i++ {
		if word[i] >= 0x80 {
			return true
		}
	}
	return false
}

type folder struct {
	lines []string
	line  string
	// lastEW is where the encoded word on this line starts, and lastEWText the
	// text it stands for; -1 when the line has none.
	lastEW     int
	lastEWText string
}

func (f *folder) add(t token) {
	if !needsEncoding(t.word) {
		if len(f.line)+len(t.space)+len(t.word) <= maxLineLength {
			f.line += t.space + t.word
			if f.lastEW >= 0 {
				// The plain words after an encoded word are part of what the
				// next one absorbs: _fold_as_ew re-encodes the whole line tail
				// from the encoded word onwards, decoding it first.
				f.lastEWText += t.space + t.word
			}
			return
		}
		f.fold(t.space)
		f.line += t.word
		return
	}
	if f.lastEW >= 0 {
		text := f.lastEWText + t.space + t.word
		word := encodeWord(text)
		if f.lastEW+len(word) <= maxLineLength {
			f.line = f.line[:f.lastEW] + word
			f.lastEWText = text
			return
		}
		// Too long combined: the tail is taken off the line and the whole text
		// re-encoded across as many lines as it needs.
		f.line = f.line[:f.lastEW]
		f.addSplit(text)
		return
	}
	// The whitespace before the word is its own token and is written as text,
	// so an encoded word that does not fit fills the rest of this line and
	// continues on the next rather than starting a line of its own.
	f.line += t.space
	word := encodeWord(t.word)
	if len(f.line)+len(word) <= maxLineLength {
		f.lastEW = len(f.line)
		f.line += word
		f.lastEWText = t.word
		return
	}
	f.addSplit(t.word)
}

// addSplit puts a word on a fresh line, in as many encoded words as it takes:
// one that still does not fit is split at a character boundary, never inside a
// character, since half of a multi-byte character in an encoded word is a
// broken subject in every client.
func (f *folder) addSplit(word string) {
	for word != "" {
		room := maxLineLength - len(f.line) - encodedWordOverhead
		chunk, rest := splitToFit(word, room)
		if chunk == "" {
			// No room on this line at all; a fresh one always has some.
			f.fold(" ")
			continue
		}
		f.lastEW = len(f.line)
		f.line += encodeWord(chunk)
		f.lastEWText = chunk
		word = rest
		if word != "" {
			f.fold(" ")
		}
	}
}

// splitToFit is the longest prefix of word whose encoded form fits in room.
func splitToFit(word string, room int) (string, string) {
	if room <= 0 {
		return "", word
	}
	for end := len(word); end > 0; {
		candidate := word[:end]
		if len(encodeWord(candidate))-encodedWordOverhead <= room {
			return candidate, word[end:]
		}
		_, size := utf8.DecodeLastRuneInString(candidate)
		end -= size
	}
	return "", word
}

// fold ends the line and starts the next one with the whitespace that was
// going to separate the two words: _steal_trailing_WSP_if_exists moves it
// rather than adding a space of its own, so a value written with two spaces
// keeps both.
func (f *folder) fold(space string) {
	f.lines = append(f.lines, f.line)
	f.line = space
	f.lastEW = -1
	f.lastEWText = ""
}

func (f *folder) done() string {
	lines := append(f.lines, f.line)
	return strings.Join(lines, "\r\n") + "\r\n"
}

// foldAddress writes From, To or Reply-To: the display name is free text and
// gets encoded words, the address itself never does.
//
// A display name is quoted or escaped by nothing here, as Python quotes
// nothing either: a name with a comma or a full stop in it goes out as it came
// in, and the three settings these values come from are short enough that the
// line does not fold. A name long enough to fold would fold on its own
// whitespace, which is where the parser breaks too.
func foldAddress(name, value string) string {
	display, address, found := strings.Cut(value, "<")
	if !found {
		return foldUnstructured(name, value)
	}
	f := folder{line: name + ":", lastEW: -1}
	for _, t := range tokenize(strings.TrimRight(display, " \t")) {
		f.add(t)
	}
	f.add(token{space: " ", word: "<" + strings.TrimLeft(address, " ")})
	return f.done()
}
