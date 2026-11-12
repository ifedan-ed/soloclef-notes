package tablesafe

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_chunker/chonkie"
	"git.danvics.com/danvics/soloclef/soloclef_chunker/recursive"
)

// The documents under testdata are synthetic, and each one's .chunks.json
// holds what the Python chunker cut from it, recorded before that chunker was
// removed. They stand in for the live corpus, whose text is not committed.
//
// The comparison is byte for byte, chunk for chunk, with no tolerance of any
// kind. Both sides now slice the document rather than rebuilding it from
// token ids, so there is nothing left that could legitimately differ: a
// difference of one byte is a boundary that moved.

// sizing is testdata/documents/sizing.json: what counted a chunk's size when
// the goldens were recorded.
type sizing struct {
	Unit      string         `json:"unit"`
	ChunkSize int            `json:"chunk_size"`
	Probes    map[string]int `json:"probes"`
}

// characterUnit is the unit string chunk_sizing.py writes when
// EMBEDDING_TOKENIZER is unset, in which case chonkie counts characters and
// recursive.Characters is the matching Sizer.
const characterUnit = "characters"

const documentsDir = "../testdata/documents"

// loadSizing reads sizing.json and returns the Sizer that reproduces it,
// skipping the test when this machine cannot supply that vocabulary.
//
// Checked rather than assumed. 512 characters and 512 BGE-M3 tokens are
// different amounts of text, so chunking the fixtures with the wrong
// vocabulary would report a difference in nearly every document and none of
// those differences would say anything about the chunker. The probes in
// sizing.json are what tells the two cases apart.
func loadSizing(t *testing.T) (sizing, recursive.Sizer) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(documentsDir, "sizing.json"))
	if err != nil {
		t.Fatalf("reading sizing.json: %v (it is recorded with the fixtures and must be committed with them)", err)
	}
	var recorded sizing
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatalf("sizing.json: %v", err)
	}
	if recorded.ChunkSize < 1 {
		t.Fatalf("sizing.json records a chunk size of %d", recorded.ChunkSize)
	}
	if len(recorded.Probes) == 0 {
		t.Fatal("sizing.json records no probes, so the vocabulary cannot be checked")
	}

	if recorded.Unit == characterUnit {
		return recorded, mustAgreeWithProbes(t, recursive.Characters{}, recorded)
	}

	path := os.Getenv("SOLOCLEF_TOKENIZER")
	if path == "" {
		t.Skipf("the goldens were recorded in %s; set SOLOCLEF_TOKENIZER to the "+
			"tokenizer.json that vocabulary comes from (docker compose cp "+
			"backend:/app/tokenizer/tokenizer.json /tmp/tokenizer.json)", recorded.Unit)
	}
	tokenizer, err := chonkie.Open(path)
	if err != nil {
		t.Fatalf("SOLOCLEF_TOKENIZER=%s: %v", path, err)
	}
	t.Cleanup(func() { _ = tokenizer.Close() })
	return recorded, mustAgreeWithProbes(t, tokenizer, recorded)
}

// mustAgreeWithProbes refuses to compare anything until the Sizer counts the
// recorded probe strings exactly as Python counted them.
func mustAgreeWithProbes(t *testing.T, sizer recursive.Sizer, recorded sizing) recursive.Sizer {
	t.Helper()
	for probe, want := range recorded.Probes {
		got, err := sizer.Count(probe)
		if err != nil {
			t.Fatalf("counting the probe %q: %v", probe, err)
		}
		if got != want {
			t.Fatalf("this Sizer counts %q as %d %s and Python counted it as %d: "+
				"it is a different vocabulary, so nothing below would mean anything",
				probe, got, recorded.Unit, want)
		}
	}
	return sizer
}

func TestTheFixtureChunksAreThePythonOnes(t *testing.T) {
	recorded, sizer := loadSizing(t)
	chunker := Chunker{Size: recorded.ChunkSize, Sizer: sizer}

	documents, err := filepath.Glob(filepath.Join(documentsDir, "*.md"))
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

			got, err := chunker.Chunks(string(text))
			if err != nil {
				t.Fatalf("Chunks: %v", err)
			}
			if len(got) != len(want) {
				t.Fatalf("got %d chunks, Python cut %d", len(got), len(want))
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("chunk %d differs: %s", i, firstDifference(got[i], want[i]))
				}
			}
		})
	}
}

// firstDifference says where two chunks part company, in bytes, without
// printing either of them whole. The fixtures are synthetic so there would be
// nothing wrong with printing them, but the same helper reads failures from
// the live corpus comparison, where there would be.
func firstDifference(got, want string) string {
	at := 0
	for at < len(got) && at < len(want) && got[at] == want[at] {
		at++
	}
	return fmt.Sprintf(
		"Go cut %d bytes and Python cut %d; they agree for %d bytes, then Go has %q and Python has %q",
		len(got), len(want), at, around(got, at), around(want, at))
}

func around(text string, at int) string {
	end := min(at+20, len(text))
	return text[at:end]
}
