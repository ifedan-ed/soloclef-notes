package chat

import (
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/markdownmemory"
)

// The prompt shows a memory document up to the length the memory routes let
// one reach. The assembly lives in another module and cannot import the
// routes' limit, so the two numbers are held equal here: a prompt that
// showed less than a document may hold would have the model replace text
// it never saw.
func TestThePromptShowsAsMuchMemoryAsADocumentMayHold(t *testing.T) {
	if assembly.MaxMemoryChars != markdownmemory.HardLimit {
		t.Fatalf("the prompt shows %d characters of memory and a document may hold %d", assembly.MaxMemoryChars, markdownmemory.HardLimit)
	}
}
