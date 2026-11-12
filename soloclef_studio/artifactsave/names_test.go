package artifactsave

import "testing"

// Moved here with the code it tests, from the API tier's deliverables
// package (TestNames there keeps the file-name slug, which stays there).
func TestDocumentNamesAndHashes(t *testing.T) {
	if got := documentName("a/b\\c\x00  d.", 1); got != "a b c d.md" {
		t.Errorf("document name = %q", got)
	}
	if got := documentName("Deck", 3); got != "Deck (3).md" {
		t.Errorf("third attempt = %q", got)
	}
	const one, two = "aaaaaaaa-0000-4000-8000-000000000001", "bbbbbbbb-0000-4000-8000-000000000002"
	content, identifier := documentHashes(one, "/documents/Deck.md", "# Deck\n")
	if len(content) != 64 || len(identifier) != 64 || content == identifier {
		t.Errorf("hashes = %s, %s", content, identifier)
	}
	// The same document at the same path is a different value for another
	// account: one account's hash cannot be recognised in another's row.
	if theirs, theirID := documentHashes(two, "/documents/Deck.md", "# Deck\n"); theirs == content || theirID == identifier {
		t.Errorf("two accounts share a hash: %s, %s", theirs, theirID)
	}
}
