// Package artifactsave writes an artifact: the document it sits on, the
// artifact row, and its stored files, as one unit.
//
// It is the API tier's chat saver moved here unchanged in what it writes
// (soloclef_api/internal/chat/deliverables keeps the formats and their
// checks, and saves through this), so that the worker's video render saves
// the same rows in the same way. Both tiers' artifacts are then listed,
// served and deleted by one reader (soloclef_api/internal/artifacts).
//
// Two ways in:
//
//   - Store.Create, for a file already in memory (a deck, a report): it
//     opens the transaction, stores the file inside its span and removes it
//     again if the rows do not commit.
//   - ReserveID and Insert, for a caller that holds its own transaction and
//     has stored its files already: the video streams its MP4 out of the
//     sandbox into the store, and must write the artifact in the same
//     transaction that marks its job ready, so a cancel cannot land between
//     the two.
package artifactsave

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// Owner is who an artifact is made for: the account and the thread. It is
// fixed by the server (the turn's principal, the job's row), never by
// anything a model wrote.
type Owner struct {
	UserID   string
	ThreadID int64
}

// NewArtifact is what Store.Create writes.
type NewArtifact struct {
	Owner      Owner
	ToolCallID string
	Title      string
	Format     string
	// Markdown is the artifact as text, stored as its document.
	Markdown string
	// Metadata is the artifact's metadata column, JSON.
	Metadata []byte
	// Filename and MimeType describe the one file; Data is its bytes. An
	// artifact that is its document and nothing else (a mind map) has no
	// file: all three are empty, and HasFile says so.
	Filename string
	MimeType string
	Data     []byte
	// Preview is a PDF rendering of the file, for a format the page cannot
	// draw itself (a Word document, a deck): the viewer shows it in place
	// of the file. It is stored as the artifact's second file, role
	// "preview", under PreviewKey's key. Empty for every other format.
	Preview         []byte
	PreviewFilename string
	PreviewKey      func(artifactID int64) string
}

// HasFile reports whether the artifact has a file to store. It is asked by
// name, not inferred from the format, so that a Database never has to know
// which formats are which.
func (a NewArtifact) HasFile() bool { return a.Filename != "" }

// Created is the rows a save produced.
type Created struct {
	ArtifactID int64
	DocumentID int64
	// FileID is 0 for an artifact with no file.
	FileID int64
	// PreviewFileID is 0 for an artifact with no preview.
	PreviewFileID int64
}

// previewMime is the type of every preview: a PDF.
const previewMime = "application/pdf"

// pathAttempts bounds the search for a free document name. An account with
// fifty decks of one title has a different problem.
const pathAttempts = 50

// documentName makes a title into the name of the artifact's document in the
// account's tree: the title, without the characters a path cannot hold,
// ending in ".md". attempt 1 is the plain name; later attempts add " (n)",
// which is how every other document in the tree breaks a collision.
func documentName(title string, attempt int) string {
	var b strings.Builder
	for _, r := range title {
		if r == '/' || r == '\\' || unicode.IsControl(r) {
			r = ' '
		}
		b.WriteRune(r)
	}
	name := strings.Trim(strings.Join(strings.Fields(b.String()), " "), ". ")
	if runes := []rune(name); len(runes) > 120 {
		name = strings.TrimSpace(string(runes[:120]))
	}
	if name == "" {
		name = "untitled"
	}
	if attempt > 1 {
		name += " (" + strconv.Itoa(attempt) + ")"
	}
	return name + ".md"
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// documentHashes are the two hashes a document row carries, computed as the
// rest of the application computes them so that its duplicate checks and its
// unique index treat this document like any other at that path.
//
// Both carry the account. The identifier is unique per account in the
// database (uq_documents_owner_identity), so the account is in the hash only
// so that one account's value cannot be recognised in another's row.
func documentHashes(userID, path, markdown string) (content, identifier string) {
	return sha256Hex(userID + ":" + markdown), sha256Hex(fmt.Sprintf("NOTE:%s:%s", path, userID))
}
