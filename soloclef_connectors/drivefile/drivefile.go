// Package drivefile is what both tiers must agree on about one Google Drive
// file: which document it is, and what it is stored as.
//
// The API makes a file's document when a person picks it in the Google
// Picker; the worker finds that same document later, when it downloads the
// file and whenever the file changes in Drive. They find it by Identity. The
// API also has to know, before anything is downloaded, what the stored
// file will be called, because the parser is chosen from that name: a
// Google Doc has no bytes of its own and arrives as an export.
package drivefile

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"strings"
)

// DocumentType is the `documents.document_type` of a Drive file.
const DocumentType = "GOOGLE_DRIVE_FILE"

// Identity is what makes two arrivals of one Drive file the same document:
// the file's id in Drive, for one owner, as sha256 of
// "GOOGLE_DRIVE_FILE:{file_id}:{user_id}". It is written to
// `documents.unique_identifier_hash`, which is unique per account.
//
// The connection is not in it: a person who disconnects Drive and connects
// again has a new connection and the same files, and must not get every
// document twice.
func Identity(fileID, userID string) string {
	sum := sha256.Sum256([]byte(DocumentType + ":" + fileID + ":" + userID))
	return hex.EncodeToString(sum[:])
}

// PlaceholderHash fills `content_hash` (NOT NULL) for a document whose
// bytes have not been downloaded. It is derived from the identity, so it is
// stable and is never the SHA-256 of any content.
func PlaceholderHash(identity string) string {
	sum := sha256.Sum256([]byte("placeholder:" + identity))
	return hex.EncodeToString(sum[:])
}

// Google's own kinds of file. A Doc, a Sheet and a Slides deck have no
// bytes to download and are exported; a folder and a shortcut are not
// files at all.
const (
	googlePrefix = "application/vnd.google-apps."
	GoogleDoc    = googlePrefix + "document"
	GoogleSheet  = googlePrefix + "spreadsheet"
	GoogleSlides = googlePrefix + "presentation"
	GoogleFolder = googlePrefix + "folder"
	Shortcut     = googlePrefix + "shortcut"
)

// export is what a Google-native file is asked for as.
type export struct{ mime, extension string }

// exports is the format each Google-native kind is exported in: the one
// that keeps the most of it for a parser. A Doc keeps its headings and
// tables as Word, a Sheet its sheets as Excel; a deck is exported as PDF
// because its slides are pages, which is what a citation points at.
var exports = map[string]export{
	GoogleDoc:    {"application/vnd.openxmlformats-officedocument.wordprocessingml.document", ".docx"},
	GoogleSheet:  {"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", ".xlsx"},
	GoogleSlides: {"application/pdf", ".pdf"},
}

// Stored is how one Drive file is kept here.
type Stored struct {
	// Name is the stored original's file name. Its extension is what the
	// indexer chooses a parser by.
	Name string
	// ContentType is the type of the stored bytes.
	ContentType string
	// ExportAs is the type to ask Drive's export for; "" means the file
	// has bytes of its own and is downloaded as it is.
	ExportAs string
}

// StoredAs is how a Drive file with this name and type is kept, and false
// for what cannot be kept at all: a folder, a shortcut, or a Google-native
// kind with no export chosen (a Form, a Drawing, a Site).
func StoredAs(name, mimeType string) (Stored, bool) {
	if !strings.HasPrefix(mimeType, googlePrefix) {
		return Stored{Name: name, ContentType: mimeType}, true
	}
	format, exported := exports[mimeType]
	if !exported {
		return Stored{}, false
	}
	// A Doc called "Plan.docx" is not stored as "Plan.docx.docx".
	if !strings.EqualFold(path.Ext(name), format.extension) {
		name += format.extension
	}
	return Stored{Name: name, ContentType: format.mime, ExportAs: format.mime}, true
}
