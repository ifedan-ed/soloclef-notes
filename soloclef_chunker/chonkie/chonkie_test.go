package chonkie

import (
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// tokenizerPath is the baked vocabulary. These tests need the real one --
// there is nothing to check about a stand-in -- so they skip without it.
//
//	SOLOCLEF_TOKENIZER=/path/to/tokenizer.json go test ./chonkie/
//
// Get it out of the running backend with:
//
//	docker compose cp backend:/app/tokenizer/tokenizer.json /tmp/tokenizer.json
func tokenizerPath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("SOLOCLEF_TOKENIZER")
	if path == "" {
		t.Skip("set SOLOCLEF_TOKENIZER to the baked tokenizer.json")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("SOLOCLEF_TOKENIZER=%s is not readable: %v", path, err)
	}
	return path
}

func open(t *testing.T) *Tokenizer {
	t.Helper()
	tok, err := Open(tokenizerPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = tok.Close() })
	return tok
}

func TestCoreVersionIsThePinnedOne(t *testing.T) {
	// The pin exists so Go merges where Python merges. If this changes, the
	// Python side's chonkie-core has to change with it, or neither should.
	if got := CoreVersion(); got != "0.9.2" {
		t.Fatalf("CoreVersion() = %q, want 0.9.2", got)
	}
}

func TestCountMatchesTheKnownAnswer(t *testing.T) {
	tok := open(t)
	// Measured against Python on 2026-11-10 with this exact vocabulary.
	for _, c := range []struct {
		text string
		want int
	}{
		{"", 0},
		{"hello world", 3},
	} {
		got, err := tok.Count(c.text)
		if err != nil {
			t.Fatalf("Count(%q): %v", c.text, err)
		}
		if got != c.want {
			t.Errorf("Count(%q) = %d, want %d", c.text, got, c.want)
		}
	}
}

func TestSplitTokensCutsEveryNTokens(t *testing.T) {
	tok := open(t)
	text := strings.Repeat("The patient presented with fever and a raised white count. ", 200)
	pieces, err := tok.SplitTokens(text, 512)
	if err != nil {
		t.Fatalf("SplitTokens: %v", err)
	}
	if len(pieces) < 2 {
		t.Fatalf("got %d pieces, expected the text to need several", len(pieces))
	}
	// Every piece but the last is the ceiling exactly.
	for i, piece := range pieces[:len(pieces)-1] {
		n, err := tok.Count(piece)
		if err != nil {
			t.Fatalf("Count(piece %d): %v", i, err)
		}
		if n != 512 {
			t.Errorf("piece %d is %d tokens, want 512", i, n)
		}
	}
}

// TestSplitTokensDoesNotRoundTrip records a property of this level that is
// easy to assume away and costly to assume wrongly.
//
// Decoding a BGE-M3 token slice does NOT give back the text it came from.
// The vocabulary is SentencePiece: its decoder renders the metaspace marker as
// a space and has no representation for a newline at all, so a decode turns
// every "\n" into " " and loses a space at some piece boundaries. On the live
// corpus this is visible: ",\n\n20\n---\n" comes back as ", 20 --- ".
//
// This is chonkie's behaviour, not this binding's -- the decoded pieces were
// checked byte for byte against Python's on a 246,503-character document and
// were identical. So it is reproduced here rather than corrected: the Python
// side writes these chunks to the database, and a Go chunker that "fixed" them
// would disagree with the corpus it has to match.
//
// What it means for callers: a chunk from this level is not a substring of its
// document, so it cannot be located in the document by searching for it. On
// the live corpus 1 chunk of 1,597 is in this state. The level is only reached
// by a run of over 512 tokens holding no paragraph break, sentence end,
// punctuation mark or space, so it is rare -- but it is silent, which is the
// part worth a test.
func TestSplitTokensDoesNotRoundTrip(t *testing.T) {
	tok := open(t)
	// One "word" long enough to reach the last level, with newlines in it.
	text := strings.Repeat("a\n", 2000)
	pieces, err := tok.SplitTokens(text, 512)
	if err != nil {
		t.Fatalf("SplitTokens: %v", err)
	}
	joined := strings.Join(pieces, "")
	if joined == text {
		t.Fatal("the decode round-tripped; if chonkie has changed this, the " +
			"comment above and the callers that rely on it need revisiting")
	}
	if strings.Contains(joined, "\n") {
		t.Error("expected every newline to be rendered as a space by the decoder")
	}
}

func TestMergeSplitsFillsUpToTheCeiling(t *testing.T) {
	tok := open(t)
	splits := make([]string, 40)
	for i := range splits {
		splits[i] = "A short sentence that is a few tokens long.\n"
	}
	counts, err := tok.CountAll(splits)
	if err != nil {
		t.Fatalf("CountAll: %v", err)
	}
	merged, mergedCounts, err := MergeSplits(splits, counts, 64)
	if err != nil {
		t.Fatalf("MergeSplits: %v", err)
	}
	if len(merged) == 0 || len(merged) >= len(splits) {
		t.Fatalf("got %d merged from %d splits, expected fewer", len(merged), len(splits))
	}
	if len(mergedCounts) != len(merged) {
		t.Fatalf("got %d merged splits but %d counts", len(merged), len(mergedCounts))
	}
	// The merge concatenates: nothing is rewritten or reordered.
	if strings.Join(merged, "") != strings.Join(splits, "") {
		t.Error("merging changed the text")
	}
	for i, m := range merged {
		n, _ := tok.Count(m)
		if n > 64 {
			t.Errorf("merged %d is %d tokens, over the 64 ceiling", i, n)
		}
	}
	// Every count the core returns is the SUM of the counts it was given,
	// which is the number chonkie branches on. It is not a recount of the
	// merged text, and on this input it is not even equal to one: see below.
	if total := sum(counts); total != sum(mergedCounts) {
		t.Errorf("the merged counts total %d, the counts going in totalled %d",
			sum(mergedCounts), total)
	}
}

// TestTheMergedCountIsASumNotARecount is the reason MergeSplits returns
// counts at all.
//
// chonkie decides whether a merged piece has to be cut again at the next
// level by comparing the core's count for it against the chunk size. That
// count is the sum of the pieces' counts. Re-counting the merged text is a
// different number with a SentencePiece vocabulary, because the leading-space
// mark on a piece depends on what came before it -- so a caller that
// recounted would recurse where chonkie does not, and cut where chonkie does
// not.
func TestTheMergedCountIsASumNotARecount(t *testing.T) {
	tok := open(t)
	// One letter per split. Each is a token on its own; joined they are a
	// handful of word pieces, so the sum and a recount cannot agree.
	splits := make([]string, 60)
	for i := range splits {
		splits[i] = "a"
	}
	counts, err := tok.CountAll(splits)
	if err != nil {
		t.Fatalf("CountAll: %v", err)
	}
	merged, mergedCounts, err := MergeSplits(splits, counts, 1024)
	if err != nil {
		t.Fatalf("MergeSplits: %v", err)
	}
	if len(merged) != 1 {
		t.Fatalf("got %d merged pieces, expected them all to fit in one", len(merged))
	}
	recounted, err := tok.Count(merged[0])
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if mergedCounts[0] != sum(counts) {
		t.Errorf("the core's count for the merged piece is %d, not the sum %d "+
			"of the counts it joined", mergedCounts[0], sum(counts))
	}
	if recounted == mergedCounts[0] {
		t.Fatalf("a recount of the merged text is also %d, so this input no "+
			"longer shows the difference the rule is about; find one that does "+
			"rather than deleting the rule", recounted)
	}
	t.Logf("the core's sum is %d and a recount of the same text is %d",
		mergedCounts[0], recounted)
}

func sum(values []int) int {
	total := 0
	for _, value := range values {
		total += value
	}
	return total
}

func TestMergeSplitsRefusesMismatchedCounts(t *testing.T) {
	if _, _, err := MergeSplits([]string{"a", "b"}, []int{1}, 10); err == nil {
		t.Error("expected an error when counts are shorter than splits")
	}
}

func TestInvalidUTF8IsAnErrorNotAReplacement(t *testing.T) {
	tok := open(t)
	// A replacement character would change the count silently, which on a
	// document means a chunk sized against text that was never there.
	bad := string([]byte{0xff, 0xfe})
	if _, err := tok.Count(bad); err == nil {
		t.Error("expected invalid UTF-8 to be refused")
	}
}

func TestAClosedTokenizerRefusesWorkRatherThanCrashing(t *testing.T) {
	tok, err := Open(tokenizerPath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := tok.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := tok.Close(); err != nil {
		t.Errorf("a second Close should be a no-op, got %v", err)
	}
	if _, err := tok.Count("hello"); err == nil {
		t.Error("expected a closed tokenizer to refuse Count")
	}
	if _, err := tok.SplitTokens("hello", 8); err == nil {
		t.Error("expected a closed tokenizer to refuse SplitTokens")
	}
}

func TestEmptyAndTinyInputs(t *testing.T) {
	tok := open(t)
	if pieces, err := tok.SplitTokens("", 512); err != nil || len(pieces) != 0 {
		t.Errorf("SplitTokens(\"\") = %v, %v; want no pieces and no error", pieces, err)
	}
	if _, err := tok.SplitTokens("hello", 0); err == nil {
		t.Error("expected size 0 to be refused")
	}
	if merged, counts, err := MergeSplits(nil, nil, 512); err != nil || merged != nil || counts != nil {
		t.Errorf("MergeSplits(nil) = %v, %v, %v; want nil, nil, nil", merged, counts, err)
	}
}

// corpusPath is a real document from the live corpus, for the checks that only
// mean something on real text. It is never committed: this repository has a
// public mirror, and the synthetic documents in ../testdata/documents are
// synthetic for that reason (../AGENTS.md).
//
//	SOLOCLEF_CORPUS=/path/to/sample.md go test ./chonkie/
func corpusPath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("SOLOCLEF_CORPUS")
	if path == "" {
		t.Skip("set SOLOCLEF_CORPUS to a real document to check against")
	}
	return path
}

// corpusText is that document, or a skip.
func corpusText(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(corpusPath(t))
	if err != nil {
		t.Skipf("SOLOCLEF_CORPUS is not readable: %v", err)
	}
	return string(body)
}

// theTextThatBreaksDecoding is the shape of the one live chunk that came back
// corrupted: a long run of short lines, so the text is full of newlines that a
// decode cannot represent. 400 copies is comfortably over any chunk size this
// chunker uses.
var theTextThatBreaksDecoding = strings.Repeat("a,\n\n20\n---\n", 400)

func TestSplitOffsetsLosesNothing(t *testing.T) {
	tok := open(t)
	// The property SplitTokens does not have: the pieces are the text.
	for name, text := range map[string]string{
		"newlines":   theTextThatBreaksDecoding,
		"no breaks":  strings.Repeat("abcdefghij", 900),
		"unicode":    strings.Repeat("αβγ δε — ζηθ ικ\n", 300),
		"one token":  "hello",
		"empty":      "",
		"whitespace": strings.Repeat(" \n\t", 500),
	} {
		pieces, err := tok.SplitTokensVerbatim(text, 512)
		if err != nil {
			t.Fatalf("%s: SplitTokensVerbatim: %v", name, err)
		}
		if got := strings.Join(pieces, ""); got != text {
			t.Errorf("%s: joining %d pieces does not give the text back "+
				"(%d bytes in, %d out)", name, len(pieces), len(text), len(got))
		}
	}
}

func TestSplitOffsetsFixesWhatSplitTokensLoses(t *testing.T) {
	tok := open(t)
	text := theTextThatBreaksDecoding

	// The old way: every newline becomes a space, so the pieces are not in
	// the document at all. This is chonkie's behaviour, reproduced, not a
	// defect of this binding -- Python does the same on the same input.
	decoded, err := tok.SplitTokens(text, 512)
	if err != nil {
		t.Fatalf("SplitTokens: %v", err)
	}
	foundDecoded := 0
	for _, piece := range decoded {
		if strings.Contains(text, piece) {
			foundDecoded++
		}
	}
	if foundDecoded == len(decoded) {
		t.Fatalf("SplitTokens returned %d pieces and all of them are in the "+
			"text -- the corruption this test is about did not happen, so "+
			"either the tokenizer or chonkie changed and the Python side "+
			"needs looking at too", len(decoded))
	}

	// The new way: every piece is in the document, at a known place.
	pieces, err := tok.SplitTokensVerbatim(text, 512)
	if err != nil {
		t.Fatalf("SplitTokensVerbatim: %v", err)
	}
	cuts, err := tok.SplitOffsets(text, 512)
	if err != nil {
		t.Fatalf("SplitOffsets: %v", err)
	}
	for i, piece := range pieces {
		if !strings.Contains(text, piece) {
			t.Fatalf("piece %d is not in the text: %q", i, piece)
		}
		// Not merely present somewhere: present at the offset it claims.
		if text[cuts[i]:cuts[i+1]] != piece {
			t.Fatalf("piece %d does not sit at its own offsets %d..%d",
				i, cuts[i], cuts[i+1])
		}
	}
}

func TestSplitOffsetsHonoursTheTokenBudget(t *testing.T) {
	tok := open(t)
	const size = 512
	for name, text := range map[string]string{
		"decode breaker": theTextThatBreaksDecoding,
		"no breaks":      strings.Repeat("abcdefghij", 900),
		"corpus":         "",
	} {
		if name == "corpus" {
			text = corpusText(t)
		}
		pieces, err := tok.SplitTokensVerbatim(text, size)
		if err != nil {
			t.Fatalf("%s: SplitTokensVerbatim: %v", name, err)
		}
		counts, err := tok.CountAll(pieces)
		if err != nil {
			t.Fatalf("%s: CountAll: %v", name, err)
		}
		for i, count := range counts {
			// A piece is cut at the start of the token that would have been
			// the 513th, so it holds 512 tokens of the whole text's encoding.
			// Re-encoding the piece on its own can differ by a token at each
			// end, because SentencePiece's leading-space mark depends on what
			// came before. One is the slack that costs nothing; a piece over
			// the budget by more than that would mean the offsets are wrong.
			if count > size+1 {
				t.Errorf("%s: piece %d of %d holds %d tokens, over the %d "+
					"budget by more than the boundary slack",
					name, i, len(pieces), count, size)
			}
		}
	}
}

func TestSplitOffsetsCutsAtTheSamePlacesOnRealText(t *testing.T) {
	tok := open(t)
	text := corpusText(t)
	const size = 512

	cuts, err := tok.SplitOffsets(text, size)
	if err != nil {
		t.Fatalf("SplitOffsets: %v", err)
	}
	if len(cuts) < 2 {
		t.Fatalf("a %d-byte document gave %d cut points", len(text), len(cuts))
	}
	if cuts[0] != 0 || cuts[len(cuts)-1] != len(text) {
		t.Fatalf("cuts run %d..%d, want 0..%d",
			cuts[0], cuts[len(cuts)-1], len(text))
	}
	total, err := tok.Count(text)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	// One piece per run of `size` tokens, give or take the dropped cut a
	// repeated offset would cause. Anything far off means the offsets are not
	// tracking the tokens.
	want := (total + size - 1) / size
	if got := len(cuts) - 1; got < want-1 || got > want+1 {
		t.Errorf("%d tokens at %d per piece should give about %d pieces, got %d",
			total, size, want, got)
	}
}

// The two splitters below need no vocabulary: they are chonkie's core, which
// works on bytes. So unlike everything above, these tests do not skip.

func pieces(t *testing.T, text string, spans []Span) []string {
	t.Helper()
	out := make([]string, 0, len(spans))
	for _, span := range spans {
		out = append(out, text[span.Start:span.End])
	}
	return out
}

func TestSplitAtPatternsLeavesTheDelimiterOnThePieceItClosed(t *testing.T) {
	text := "Hello. World. Test."
	spans, err := SplitAtPatterns(text, []string{". "}, IncludePrev, 0)
	if err != nil {
		t.Fatalf("SplitAtPatterns: %v", err)
	}
	got := pieces(t, text, spans)
	want := []string{"Hello. ", "World. ", "Test."}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("piece %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// Leftmost-first matching: where two patterns start at the same byte, the one
// listed FIRST wins, not the longest. chonkie's paragraph level relies on it
// -- "\n\n" is listed before "\n" so a blank line is one cut, not two.
func TestSplitAtPatternsPrefersTheFirstPatternListed(t *testing.T) {
	text := "alpha\n\nbeta"
	spans, err := SplitAtPatterns(text, []string{"\n\n", "\n"}, IncludePrev, 0)
	if err != nil {
		t.Fatalf("SplitAtPatterns: %v", err)
	}
	got := pieces(t, text, spans)
	if len(got) != 2 || got[0] != "alpha\n\n" || got[1] != "beta" {
		t.Errorf("got %q, want [\"alpha\\n\\n\" \"beta\"]", got)
	}
}

// min_characters_per_chunk is named in characters and measured in BYTES. This
// is the case that tells the two apart: each piece below is 6 characters and
// 13 bytes, so a minimum of 12 is reached by every piece in bytes and by none
// of them in characters.
//
// A cut is only taken where the piece it closes and the NEXT piece both reach
// the minimum, so counting characters here would absorb the whole text into
// one piece instead of cutting at all.
func TestTheMinimumPieceIsCountedInBytes(t *testing.T) {
	const piece = "ααααα—" // 5 two-byte letters and a three-byte dash
	if len(piece) != 13 || utf8.RuneCountInString(piece) != 6 {
		t.Fatalf("the fixture is %d bytes and %d characters, expected 13 and 6",
			len(piece), utf8.RuneCountInString(piece))
	}
	text := strings.Repeat(piece, 6)
	spans, err := SplitAtPatterns(text, []string{"—"}, IncludePrev, 12)
	if err != nil {
		t.Fatalf("SplitAtPatterns: %v", err)
	}
	if len(spans) != 6 {
		t.Fatalf("got %d pieces, want 6 -- a byte minimum, not a character one", len(spans))
	}
	for i, span := range spans {
		if text[span.Start:span.End] != piece {
			t.Errorf("piece %d = %q, want %q", i, text[span.Start:span.End], piece)
		}
	}
}

func TestSplitAtDelimitersTakesAByteSet(t *testing.T) {
	text := "one two three"
	spans, err := SplitAtDelimiters(text, " ", IncludePrev, 0)
	if err != nil {
		t.Fatalf("SplitAtDelimiters: %v", err)
	}
	got := pieces(t, text, spans)
	want := []string{"one ", "two ", "three"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("piece %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestTheSplitsReconstructTheText(t *testing.T) {
	text := "A line.\nAnother — with punctuation; and more.\n\nA paragraph after it.\n"
	for name, spans := range map[string][]Span{
		"patterns":   mustSplitPatterns(t, text, []string{"\n\n", "\r\n", "\n", "\r"}),
		"delimiters": mustSplitDelimiters(t, text, " "),
	} {
		joined := ""
		for _, span := range spans {
			joined += text[span.Start:span.End]
		}
		if joined != text {
			t.Errorf("%s: the pieces do not reconstruct the text", name)
		}
	}
}

func mustSplitPatterns(t *testing.T, text string, patterns []string) []Span {
	t.Helper()
	spans, err := SplitAtPatterns(text, patterns, IncludePrev, 24)
	if err != nil {
		t.Fatal(err)
	}
	return spans
}

func mustSplitDelimiters(t *testing.T, text, delimiters string) []Span {
	t.Helper()
	spans, err := SplitAtDelimiters(text, delimiters, IncludePrev, 24)
	if err != nil {
		t.Fatal(err)
	}
	return spans
}

func TestTheSplittersRefuseWhatTheyCannotAnswer(t *testing.T) {
	if _, err := SplitAtPatterns("text", nil, IncludePrev, 0); err == nil {
		t.Error("expected no patterns to be refused rather than treated as one piece")
	}
	if _, err := SplitAtPatterns("text", []string{""}, IncludePrev, 0); err == nil {
		t.Error("expected an empty pattern to be refused")
	}
	if _, err := SplitAtDelimiters("text", "", IncludePrev, 0); err == nil {
		t.Error("expected no delimiters to be refused")
	}
	// An unnamed include_delim is a mistake, not a request for the default.
	if _, err := SplitAtPatterns("text", []string{"e"}, IncludeDelim(7), 0); err == nil {
		t.Error("expected an unknown include_delim to be refused")
	}
	if spans, err := SplitAtPatterns("", []string{"e"}, IncludePrev, 0); err != nil || spans != nil {
		t.Errorf("SplitAtPatterns(\"\") = %v, %v; want no spans and no error", spans, err)
	}
}
