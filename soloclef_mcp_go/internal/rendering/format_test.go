package rendering_test

import (
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/rendering"
)

// Counted in characters, not bytes: the Python original measures Python string
// length, so a byte count would cut a different place in any non-ASCII text.
func TestClipCountsCharactersNotBytes(t *testing.T) {
	text := strings.Repeat("é", 10)
	if got := rendering.Clip(text, 10); got != text {
		t.Errorf("a 10-character string was clipped at a limit of 10: %q", got)
	}
	got := rendering.Clip(text, 4)
	const want = "éééé\n\n… [6 more characters truncated]"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestClipLeavesShortTextAlone(t *testing.T) {
	if got := rendering.Clip("short", 100); got != "short" {
		t.Errorf("got %q", got)
	}
}
