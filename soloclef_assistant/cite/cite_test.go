package cite

import (
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
)

func TestRewrite(t *testing.T) {
	lookup := FromSources([]citation.Source{
		{Kind: Chunk, Ref: "481"},
		{Kind: Web, Ref: "https://example.org/a?b=1"},
	})
	for _, c := range []struct{ name, in, want string }{
		{"no labels", "Plain text.", "Plain text."},
		{"a chunk", "Mitosis has four phases [1].", "Mitosis has four phases [citation:481]."},
		{"stacked", "Both agree [1][2].", "Both agree [citation:481][citation:https://example.org/a?b=1]."},
		{"spaces inside", "See [ 2 ].", "See [citation:https://example.org/a?b=1]."},
		{"an invented number is removed", "True [7]. Also [1].", "True . Also [citation:481]."},
		{"zero is not a label the registry has", "x [0]", "x "},
		{"inline code is left alone", "Use `a[1]` here [1].", "Use `a[1]` here [citation:481]."},
		{"a fence is left alone", "```go\nx := a[1]\n```\nSo [1].", "```go\nx := a[1]\n```\nSo [citation:481]."},
		{"double backticks", "``a ` [1]`` and [2]", "``a ` [1]`` and [citation:https://example.org/a?b=1]"},
		{"an unclosed backtick is not code", "it`s [1]", "it`s [citation:481]"},
		{"a markdown link is not a label", "[text](u) and [1a] and [x]", "[text](u) and [1a] and [x]"},
		{"an already written marker is kept", "[citation:481]", "[citation:481]"},
		{"a huge number cannot overflow", "[99999999999999999999]", "[99999999999999999999]"},
		{"an open bracket at the end", "tail [", "tail ["},
		{"unicode around", "π ≈ 3,14 [1] — «да»", "π ≈ 3,14 [citation:481] — «да»"},
	} {
		if got := Rewrite(c.in, lookup); got != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.name, got, c.want)
		}
	}
}

func TestRewriteReadsTheRegistryItIsGiven(t *testing.T) {
	reg := citation.NewRegistry()
	n := reg.Number(citation.Source{Kind: Chunk, Ref: "9"})
	if got := Rewrite("a [1]", reg.Lookup); n != 1 || got != "a [citation:9]" {
		t.Fatalf("n=%d got %q", n, got)
	}
}

func BenchmarkRewrite(b *testing.B) {
	lookup := FromSources([]citation.Source{{Kind: Chunk, Ref: "481"}, {Kind: Chunk, Ref: "482"}})
	text := strings.Repeat("A sentence of ordinary length that ends with a source [1][2]. `code[1]` ", 60)
	b.SetBytes(int64(len(text)))
	b.ReportAllocs()
	for b.Loop() {
		Rewrite(text, lookup)
	}
}
