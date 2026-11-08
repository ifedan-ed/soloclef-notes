package recursive

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// Every want below is what chonkie 1.6.0 on chonkie-core 0.9.2 produced for
// the same input, in the backend container, at chunk_size=512 with the
// `character` tokenizer. The lengths are in characters, and a chunker that
// loses or rewrites a character fails the reconstruction check instead, so a
// matching list of lengths is a matching set of boundaries.
func TestTheCutsAreTheOnesChonkieMakes(t *testing.T) {
	emDash := "—"
	cases := []struct {
		name string
		text string
		want []int
	}{
		{"empty", "", nil},
		{"shorter than the ceiling", "Hello world.", []int{12}},
		{"no delimiter anywhere", strings.Repeat("a", 1200), []int{512, 512, 176}},
		{
			"a single word longer than the ceiling",
			"Intro sentence here. " + strings.Repeat("z", 700) +
				" tail words follow after the giant token.",
			[]int{512, 250},
		},
		{"ending exactly on the ceiling", strings.Repeat("a", 512), []int{512}},
		{"one character past the ceiling", strings.Repeat("a", 513), []int{512, 1}},
		{
			"consecutive separators",
			strings.Repeat("a", 300) + "\n\n\n\n" + strings.Repeat("b", 300),
			[]int{304, 300},
		},
		{
			"trailing whitespace",
			strings.Repeat("a", 600) + "   \n\n   ",
			[]int{512, 96},
		},
		{
			"CRLF",
			strings.Repeat("a", 300) + "\r\n" + strings.Repeat("b", 300) +
				"\r\n\r\n" + strings.Repeat("c", 300),
			[]int{302, 304, 300},
		},
		// The em dash is both a level-2 delimiter and three bytes wide, so
		// this is the case that fails when the shortest-piece rule is
		// counted in characters: chonkie-core counts it in bytes and cuts
		// every eight dashes, not every twenty-four.
		{"multi-byte delimiters", strings.Repeat(emDash, 1200), []int{512, 512, 176}},
		{
			"multi-byte words",
			strings.Repeat(strings.Repeat(emDash, 10)+" ", 120),
			[]int{506, 506, 308},
		},
		{
			"sentence ends",
			strings.Repeat("This is a sentence that runs on for a while. ", 40),
			[]int{495, 495, 495, 315},
		},
		{
			"pause punctuation",
			strings.Repeat("alpha, beta; gamma: delta (epsilon) [zeta] {eta} | theta ~ iota - kappa ... lambda ", 20),
			[]int{491, 498, 498, 173},
		},
		{
			"a short tail after a paragraph break",
			strings.Repeat("a", 600) + "\n\ntiny",
			[]int{512, 94},
		},
		{"many tiny paragraphs", strings.Repeat("ab\n", 400), []int{512, 512, 176}},
		{
			"mixed scripts",
			strings.Repeat("é中文 café naïve — こんにちは ", 60),
			[]int{499, 506, 375},
		},
		{"non-breaking spaces", strings.Repeat("word ", 300), []int{512, 512, 476}},
	}

	chunker := Chunker{Size: DefaultChunkSize}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks := chunker.Chunks(tc.text)
			lengths := make([]int, len(chunks))
			for i, chunk := range chunks {
				lengths[i] = utf8.RuneCountInString(chunk)
			}
			if !equal(lengths, tc.want) {
				t.Errorf("chunk lengths = %v, chonkie's = %v", lengths, tc.want)
			}
			if joined := strings.Join(chunks, ""); joined != tc.text {
				t.Errorf("the chunks do not reconstruct the input (%d chars in, %d out)",
					utf8.RuneCountInString(tc.text), utf8.RuneCountInString(joined))
			}
		})
	}
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
