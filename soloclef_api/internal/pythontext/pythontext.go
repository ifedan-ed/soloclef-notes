// Package pythontext is Python's own string behaviour where Go's differs, for
// the routes whose answer depends on it.
//
// It exists for the same reason internal/pythonjson does: a mirror of one
// language's idea of a string, kept in one place so that two slices cannot
// spell it two ways. `str.strip()` is the first member because two slices
// already need it — internal/markdownmemory strips a memory document and
// internal/agentrules strips a rule's permission and pattern before it
// validates them — and because it is not strings.TrimSpace: Python's
// `str.isspace()` is the Unicode White_Space property **plus** the four C0
// separators U+001C–U+001F, which Go's unicode.IsSpace does not include.
//
// The difference is reachable from a request. A new agent permission rule
// (internal/agentrules) with a permission of
// "tool:x\u001c" is stripped to "tool:x" there and accepted; trimmed with
// strings.TrimSpace it keeps the separator, fails the permission pattern and is
// a 400. The vectors are in pythontext_test.go.
package pythontext

import (
	"strings"
	"unicode"
)

// IsSpace is str.isspace() for one character.
func IsSpace(r rune) bool {
	if r >= 0x1c && r <= 0x1f {
		return true
	}
	return unicode.IsSpace(r)
}

// Strip is str.strip() with no argument.
func Strip(text string) string {
	return strings.TrimFunc(text, IsSpace)
}

// RightStrip is str.rstrip() with no argument.
func RightStrip(text string) string {
	return strings.TrimRightFunc(text, IsSpace)
}
