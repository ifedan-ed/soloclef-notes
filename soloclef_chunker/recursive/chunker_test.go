package recursive

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_chunker/chonkie"
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

	// Characters{} is chonkie's `character` tokenizer, which is what the app
	// counted with when these answers were recorded.
	chunker := Chunker{Size: DefaultChunkSize, Sizer: Characters{}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			chunks, err := chunker.Chunks(tc.text)
			if err != nil {
				t.Fatalf("Chunks: %v", err)
			}
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

// A zero Chunker refuses rather than picking a size or a vocabulary. 512
// characters and 512 BGE-M3 tokens are different amounts of text, so a
// guessed unit is a corpus that silently does not match its index.
func TestAChunkerWithNoSizingRefuses(t *testing.T) {
	if _, err := (Chunker{Size: DefaultChunkSize}).Chunks("text"); err == nil {
		t.Error("expected a Chunker with no Sizer to refuse")
	}
	if _, err := (Chunker{Sizer: Characters{}}).Chunks("text"); err == nil {
		t.Error("expected a Chunker with no Size to refuse")
	}
	if _, err := (Chunker{Size: 0, Sizer: Characters{}}).Chunks(""); err == nil {
		t.Error("expected a size of 0 to be refused even on empty input")
	}
}

// brokenSizer is a Sizer whose cut points do not describe the text it was
// given. cutAtTokens must refuse them: a cut point outside the text would
// slice a string in the wrong place, and a chunk cut in the wrong place is
// the one failure this package exists to prevent.
type brokenSizer struct {
	cuts []int
}

func (brokenSizer) Count(text string) (int, error) { return len(text) + 1_000_000, nil }

func (b brokenSizer) SplitOffsets(string, int) ([]int, error) { return b.cuts, nil }

func TestCutPointsThatDoNotDescribeTheTextAreRefused(t *testing.T) {
	// Count is huge, so every level recurses and the token level is reached.
	text := "a text with no delimiter"
	for name, cuts := range map[string][]int{
		"no cut points at all":   {},
		"does not start at 0":    {1, len(text)},
		"does not reach the end": {0, 3},
		"runs past the end":      {0, len(text) + 1},
		"goes backwards":         {0, 10, 5, len(text)},
		"repeats a cut point":    {0, 10, 10, len(text)},
	} {
		t.Run(name, func(t *testing.T) {
			chunker := Chunker{Size: 4, Sizer: brokenSizer{cuts: cuts}}
			if _, err := chunker.Chunks(text); err == nil {
				t.Errorf("expected cut points %v to be refused", cuts)
			}
		})
	}
}

func TestCharactersCountsCodePointsNotBytes(t *testing.T) {
	// Six characters, thirteen bytes. chonkie's character tokenizer counts
	// six, and so must this.
	const text = "ααααα—"
	if got, err := (Characters{}).Count(text); err != nil || got != 6 {
		t.Errorf("Count(%q) = %d, %v; want 6", text, got, err)
	}
	cuts, err := (Characters{}).SplitOffsets(strings.Repeat(text, 3), 6)
	if err != nil {
		t.Fatalf("SplitOffsets: %v", err)
	}
	// Three pieces of six characters each: 0, 13, 26, 39.
	want := []int{0, 13, 26, 39}
	if !equal(cuts, want) {
		t.Errorf("SplitOffsets = %v, want %v", cuts, want)
	}
}

func TestCharactersOnEmptyTextGivesNoPieces(t *testing.T) {
	cuts, err := (Characters{}).SplitOffsets("", 512)
	if err != nil {
		t.Fatalf("SplitOffsets: %v", err)
	}
	if len(cuts) != 1 || cuts[0] != 0 {
		t.Errorf("SplitOffsets(\"\") = %v, want a single 0 and so no pieces", cuts)
	}
}

// realTokenizer is the baked vocabulary, or a skip. The tests above need no
// vocabulary file; the ones below are about what a real one does, and there
// is nothing to learn from a stand-in.
//
//	SOLOCLEF_TOKENIZER=/path/to/tokenizer.json go test ./recursive/
func realTokenizer(t *testing.T) *chonkie.Tokenizer {
	t.Helper()
	path := os.Getenv("SOLOCLEF_TOKENIZER")
	if path == "" {
		t.Skip("set SOLOCLEF_TOKENIZER to the baked tokenizer.json")
	}
	tokenizer, err := chonkie.Open(path)
	if err != nil {
		t.Fatalf("SOLOCLEF_TOKENIZER=%s: %v", path, err)
	}
	t.Cleanup(func() { _ = tokenizer.Close() })
	return tokenizer
}

// TestTheLastLevelCutsTheTextInsteadOfRebuildingIt is the defect that forced
// the last recursion level to be rewritten on both sides.
//
// chonkie's own last level encodes the text, slices the ids and decodes each
// slice back. With a SentencePiece vocabulary that is lossy: there is no
// newline token and a leading space is carried as a mark on the piece, so
// every newline came back as a space and a space on a piece boundary
// vanished. A chunk that is not a substring of its document cannot be located
// in it, and a citation's line range is exactly that lookup.
//
// The text below reaches that level because it holds no paragraph break, no
// sentence end, no pause punctuation and no space -- only newlines, which the
// paragraph level cuts into pieces far too short to stand alone.
func TestTheLastLevelCutsTheTextInsteadOfRebuildingIt(t *testing.T) {
	tokenizer := realTokenizer(t)
	text := strings.Repeat("a\n", 4000)

	chunks, err := Chunker{Size: DefaultChunkSize, Sizer: tokenizer}.Chunks(text)
	if err != nil {
		t.Fatalf("Chunks: %v", err)
	}
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, expected the text to need several", len(chunks))
	}
	if joined := strings.Join(chunks, ""); joined != text {
		t.Fatalf("the chunks do not reconstruct the document (%d bytes in, %d out)",
			len(text), len(joined))
	}
	at := 0
	for i, chunk := range chunks {
		// Not merely present somewhere: present where it came from.
		if at+len(chunk) > len(text) || text[at:at+len(chunk)] != chunk {
			t.Fatalf("chunk %d is not the document's own bytes at offset %d", i, at)
		}
		at += len(chunk)
	}
}

// With a real vocabulary a chunk's size is counted in its tokens, so a
// document that fits in 512 characters can still need several chunks -- and
// one that needs several at 512 characters can fit in one.
func TestTheUnitIsWhateverTheSizerCounts(t *testing.T) {
	tokenizer := realTokenizer(t)
	text := strings.Repeat("The specimen showed a raised level. ", 60)

	inCharacters, err := Chunker{Size: DefaultChunkSize, Sizer: Characters{}}.Chunks(text)
	if err != nil {
		t.Fatalf("Chunks (characters): %v", err)
	}
	inTokens, err := Chunker{Size: DefaultChunkSize, Sizer: tokenizer}.Chunks(text)
	if err != nil {
		t.Fatalf("Chunks (tokens): %v", err)
	}
	if len(inCharacters) == len(inTokens) {
		t.Fatalf("512 characters and 512 tokens both gave %d chunks for this "+
			"text, so it no longer shows that the unit matters", len(inTokens))
	}
	for name, chunks := range map[string][]string{
		"characters": inCharacters,
		"tokens":     inTokens,
	} {
		if joined := strings.Join(chunks, ""); joined != text {
			t.Errorf("%s: the chunks do not reconstruct the input", name)
		}
	}
}
