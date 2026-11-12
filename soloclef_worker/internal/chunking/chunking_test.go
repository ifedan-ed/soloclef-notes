package chunking

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/embedding"
)

func characters(size int) embedding.Profile {
	return embedding.Profile{
		Name: "test", Model: "m", Dimension: 4, Sizer: embedding.CharacterSizer,
		ChunkSize: size, MaxInput: size * embedding.OversizeFactor, BatchSize: 1, Concurrency: 1,
	}
}

func TestPiecesCarryTheSpanTheChunkerCutThemFrom(t *testing.T) {
	chunker, err := Open(characters(80), "")
	if err != nil {
		t.Fatal(err)
	}
	defer chunker.Close()
	markdown := strings.Repeat("Give the dose once daily with food. ", 10) +
		"\n\n| Drug | Dose |\n| --- | --- |\n| A | 1 mg |\n| B | 2 mg |\n| C | 3 mg |\n| D | 4 mg |\n| E | 5 mg |\n"

	pieces, err := chunker.Pieces(markdown)
	if err != nil {
		t.Fatal(err)
	}
	if len(pieces) < 4 {
		t.Fatalf("%d pieces", len(pieces))
	}
	for index, piece := range pieces {
		// The span is the piece's own content: all of a prose piece, and the
		// rows of a table piece (which may have the header put above them).
		if !strings.HasSuffix(piece.Text, markdown[piece.Start:piece.End]) {
			t.Errorf("piece %d does not end in its span: %q vs %q", index, piece.Text, markdown[piece.Start:piece.End])
		}
	}
}

func TestAProfileThatCountsTokensNeedsItsTokenizerFile(t *testing.T) {
	profile := characters(80)
	profile.Sizer, profile.Tokenizer = embedding.TokenizerSizer, "bge-m3"

	if _, err := Open(profile, ""); err == nil {
		t.Error("a chunker was built with no tokenizer directory")
	}
	// A directory with no file in it: refused, not sized in characters.
	_, err := Open(profile, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), filepath.Join("bge-m3", TokenizerFile)) {
		t.Errorf("err = %v, want it to name the missing file", err)
	}
}

func TestAnUnknownSizerIsRefused(t *testing.T) {
	profile := characters(80)
	profile.Sizer = "words"
	if _, err := Open(profile, ""); err == nil {
		t.Fatal("a chunker was built for a sizer that does not exist")
	}
}

// With a real vocabulary: set SOLOCLEF_INDEX_TEST_TOKENIZER_DIR to a folder
// holding bge-m3/tokenizer.json (the backend image bakes the file).
func TestTheRealProfileCutsAtItsOwnTokens(t *testing.T) {
	directory := os.Getenv("SOLOCLEF_INDEX_TEST_TOKENIZER_DIR")
	if directory == "" {
		t.Skip("set SOLOCLEF_INDEX_TEST_TOKENIZER_DIR to run this against the real vocabulary")
	}
	profile, err := embedding.ProfileNamed("bge-m3")
	if err != nil {
		t.Fatal(err)
	}
	chunker, err := Open(profile, directory)
	if err != nil {
		t.Fatal(err)
	}
	defer chunker.Close()
	// About 4,000 tokens of prose: several chunks at 512 tokens, and far
	// fewer than the 40 a character count of 512 would cut.
	markdown := strings.Repeat("The quick brown fox jumps over the lazy dog near the river bank. ", 300)
	pieces, err := chunker.Pieces(markdown)
	if err != nil {
		t.Fatal(err)
	}
	if len(pieces) < 4 || len(pieces) > 20 {
		t.Errorf("%d pieces for about 4,000 tokens at 512 a chunk", len(pieces))
	}
}
