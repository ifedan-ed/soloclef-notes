package tablesafe

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_chunker/recursive"
)

// The documents under testdata are synthetic, and each one's .chunks.json
// holds what the Python chunker cut from it, recorded by
// scripts/record_fixture_chunks.sh in the backend container. They stand in
// for the live corpus, which is compared by scripts/compare_corpus.sh and
// whose text is not committed.
func TestTheFixtureChunksAreThePythonOnes(t *testing.T) {
	documents, err := filepath.Glob(filepath.Join("..", "testdata", "documents", "*.md"))
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) == 0 {
		t.Fatal("no fixture documents found")
	}

	for _, path := range documents {
		t.Run(strings.TrimSuffix(filepath.Base(path), ".md"), func(t *testing.T) {
			text, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			golden, err := os.ReadFile(strings.TrimSuffix(path, ".md") + ".chunks.json")
			if err != nil {
				t.Fatal(err)
			}
			var want []string
			if err := json.Unmarshal(golden, &want); err != nil {
				t.Fatal(err)
			}

			got := Chunks(string(text), recursive.DefaultChunkSize)
			if len(got) != len(want) {
				t.Fatalf("got %d chunks, Python cut %d", len(got), len(want))
			}
			offset := 0
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("chunk %d differs around character offset %d: got %d characters ending %q, Python cut %d ending %q",
						i, offset, len([]rune(got[i])), lastRunes(got[i]), len([]rune(want[i])), lastRunes(want[i]))
				}
				offset += len([]rune(got[i]))
			}
		})
	}
}

func lastRunes(chunk string) string {
	runes := []rune(chunk)
	if len(runes) > 20 {
		runes = runes[len(runes)-20:]
	}
	return string(runes)
}
