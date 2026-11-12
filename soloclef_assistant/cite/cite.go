// Package cite turns the labels a model writes, "[3]", into the markers the
// apps draw as source chips, "[citation:<where>]".
//
// While an answer streams the reader sees the bare label; the rewrite is
// applied to the text that is saved, which is what a reloaded page renders.
// The numbering itself is not this package's: it is the thread's
// citation.Registry, shared by every agent in the run, so a label means the
// same source whoever wrote it.
package cite

import (
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
)

// The kinds of source the chat's tools register. The ref of each is exactly
// what goes after "citation:" in the marker, so adding a kind is adding a
// constant here and nothing else: there is no per-kind formatting to forget.
const (
	// Chunk is a passage of one of the user's own documents; its ref is the
	// chunk's id, which the web app resolves through /documents/by-chunk.
	Chunk = "kb_chunk"
	// Web is a page or a paper; its ref is the address.
	Web = "web_result"
)

// Lookup resolves a label's number to its source. citation.Registry.Lookup
// and a saved list both fit.
type Lookup func(n int) (citation.Source, bool)

// FromSources is the Lookup over a saved list, where number n is entry n-1.
func FromSources(sources []citation.Source) Lookup {
	return func(n int) (citation.Source, bool) {
		if n < 1 || n > len(sources) {
			return citation.Source{}, false
		}
		return sources[n-1], true
	}
}

// Rewrite returns text with every label replaced by its marker.
//
// A label whose number the registry does not know is removed: the model
// invented it, and a bracketed number that leads nowhere reads as a citation
// the reader cannot check. Code is left exactly as written, fenced or inline,
// because "[0]" in code is an index, not a source.
func Rewrite(text string, lookup Lookup) string {
	// Most answers to a casual question cite nothing; do not copy them.
	if !strings.Contains(text, "[") {
		return text
	}
	var out strings.Builder
	out.Grow(len(text) + 64)
	for i := 0; i < len(text); {
		switch c := text[i]; {
		case c == '`':
			end := codeEnd(text, i)
			out.WriteString(text[i:end])
			i = end
		case c == '[':
			n, end, ok := label(text, i)
			if !ok {
				out.WriteByte(c)
				i++
				continue
			}
			if src, known := lookup(n); known && src.Ref != "" {
				out.WriteString("[citation:")
				out.WriteString(src.Ref)
				out.WriteByte(']')
			}
			i = end
		default:
			out.WriteByte(c)
			i++
		}
	}
	return out.String()
}

// label reads "[", optional spaces, digits, optional spaces, "]" at i, and
// returns the number and the index after the bracket.
func label(text string, i int) (n, end int, ok bool) {
	j := i + 1
	for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
		j++
	}
	start := j
	for j < len(text) && text[j] >= '0' && text[j] <= '9' {
		j++
	}
	// Nine digits is far past any registry and keeps Atoi from overflowing.
	if j == start || j-start > 9 {
		return 0, 0, false
	}
	n, _ = strconv.Atoi(text[start:j])
	for j < len(text) && (text[j] == ' ' || text[j] == '\t') {
		j++
	}
	if j >= len(text) || text[j] != ']' {
		return 0, 0, false
	}
	return n, j + 1, true
}

// codeEnd returns the index just past the code span or fence that opens at
// i, where text[i] is a backtick. A span closes at the next run of the same
// number of backticks (CommonMark's rule, which also covers a ``` fence);
// one that never closes is not code, and only its backticks are skipped.
func codeEnd(text string, i int) int {
	run := i
	for run < len(text) && text[run] == '`' {
		run++
	}
	width := run - i
	for j := run; j < len(text); {
		if text[j] != '`' {
			j++
			continue
		}
		k := j
		for k < len(text) && text[k] == '`' {
			k++
		}
		if k-j == width {
			return k
		}
		j = k
	}
	return run
}
