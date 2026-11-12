package deliverables

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"
	"unicode"

	filestore "git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// FormatFlashcards is a flashcard deck. The string is the one the artifacts
// table and the web viewer registry use.
const FormatFlashcards = "flashcards"

// Database writes an artifact's rows and its file as one unit: either the
// document, the artifact, the stored file and the file's row all exist
// afterwards, or none does. key is given the new artifact's id and returns
// the storage key for its file.
//
// For an artifact with no file (NewArtifact.HasFile is false) there are only
// the document and the artifact rows: nothing is stored, key is not called
// and Created.FileID is 0. An artifact with a Preview has a second stored
// file and a second file row, inside the same unit.
type Database interface {
	Create(ctx context.Context, artifact NewArtifact, files filestore.Writer, key func(artifactID int64) string) (Created, error)
}

// Saver saves artifacts. It holds no per-turn state.
type Saver struct {
	Rows  Database
	Files filestore.Writer
	// Index asks for the artifact's document to be chunked and embedded so
	// the account's search finds it. It runs after the save has committed.
	Index func(ctx context.Context, documentID int64, userID string) error
	// NewID returns a fresh random name for a stored file (32 hex digits).
	NewID  func() string
	Logger *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (s Saver) Check() error {
	switch {
	case s.Rows == nil:
		return errors.New("deliverables: Rows is not set")
	case s.Files == nil:
		return errors.New("deliverables: Files is not set")
	case s.Index == nil:
		return errors.New("deliverables: Index is not set")
	case s.NewID == nil:
		return errors.New("deliverables: NewID is not set")
	case s.Logger == nil:
		return errors.New("deliverables: Logger is not set")
	}
	return nil
}

// SavedFile and Saved are what a save answers: the shape the page's
// save_artifact card draws from, and what the model is told.
type SavedFile struct {
	FileID    int64  `json:"file_id"`
	Role      string `json:"role"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mime_type"`
	SizeBytes int    `json:"size_bytes"`
}

type Saved struct {
	Status     string      `json:"status"`
	ArtifactID int64       `json:"artifact_id"`
	Generation int         `json:"generation"`
	Title      string      `json:"title"`
	Format     string      `json:"format"`
	Files      []SavedFile `json:"files"`
}

// The value of the artifacts.metadata column for everything saved here. The
// artifact was checked against the whole of its format's rules before it
// got here, which is what "verified" records; the spelling, spaces
// included, is the one the column has always held.
const verifiedMetadata = `{"verification": {"verified": true, "reason": null}}`

// jsonMime is the type of a deck's and a quiz's file.
const jsonMime = "application/json"

// SaveFlashcards stores a checked deck as a new artifact.
func (s Saver) SaveFlashcards(ctx context.Context, owner Owner, toolCallID string, deck Deck) (Saved, error) {
	data, err := deck.File()
	if err != nil {
		return Saved{}, err
	}
	return s.save(ctx, NewArtifact{
		Owner: owner, ToolCallID: toolCallID, Title: deck.Title, Format: FormatFlashcards,
		Markdown: deck.Markdown(), Metadata: []byte(verifiedMetadata),
		Filename: slug(deck.Title, FormatFlashcards) + ".json", MimeType: jsonMime, Data: data,
	})
}

// SaveQuiz stores a checked quiz as a new artifact: the same rows and the
// same kind of file as a deck, under its own format.
func (s Saver) SaveQuiz(ctx context.Context, owner Owner, toolCallID string, quiz Quiz) (Saved, error) {
	data, err := quiz.File()
	if err != nil {
		return Saved{}, err
	}
	return s.save(ctx, NewArtifact{
		Owner: owner, ToolCallID: toolCallID, Title: quiz.Title, Format: FormatQuiz,
		Markdown: quiz.Markdown(), Metadata: []byte(verifiedMetadata),
		Filename: slug(quiz.Title, FormatQuiz) + ".json", MimeType: jsonMime, Data: data,
	})
}

// SaveMindMap stores a checked mind map as a new artifact. It has no file:
// the map is its Markdown, which is the artifact's document, and that is
// what the viewer draws and what the download button sends.
func (s Saver) SaveMindMap(ctx context.Context, owner Owner, toolCallID string, mindMap MindMap) (Saved, error) {
	return s.save(ctx, NewArtifact{
		Owner: owner, ToolCallID: toolCallID, Title: mindMap.Title, Format: FormatMindMap,
		Markdown: mindMap.Markdown, Metadata: []byte(verifiedMetadata),
	})
}

// pdfMime is the type of a PDF report and of every preview.
const pdfMime = "application/pdf"

// BuiltDocument is a file built in the sandbox that passed verification,
// ready to store.
type BuiltDocument struct {
	// Format is one of the built formats (documents.go).
	Format string
	Title  string
	// Markdown is the document's content as text: what search indexes and
	// what a screen reader is given.
	Markdown string
	Data     []byte
	// Preview is the PDF the pages were checked on, for a Word document
	// and a deck; nil otherwise.
	Preview []byte
}

// SaveDocument stores a built document as a new artifact: the file, and for
// a Word document or a deck its PDF preview beside it.
func (s Saver) SaveDocument(ctx context.Context, owner Owner, toolCallID string, document BuiltDocument) (Saved, error) {
	format, known := documentFormats[document.Format]
	if !known {
		return Saved{}, fmt.Errorf("deliverables: %q is not a built format", document.Format)
	}
	// Every check is measurement now, made before the save is allowed, so
	// a saved document is always a verified one.
	metadata := []byte(verifiedMetadata)
	name := slug(document.Title, document.Format)
	artifact := NewArtifact{
		Owner: owner, ToolCallID: toolCallID, Title: document.Title, Format: document.Format,
		Markdown: document.Markdown, Metadata: metadata,
		Filename: name + format.suffix, MimeType: format.mime, Data: document.Data,
	}
	if len(document.Preview) > 0 {
		artifact.Preview, artifact.PreviewFilename = document.Preview, name+".pdf"
		artifact.PreviewKey = func(artifactID int64) string {
			return "artifacts/" + owner.UserID + "/" + strconv.FormatInt(artifactID, 10) + "/preview/" + s.NewID() + ".pdf"
		}
	}
	return s.save(ctx, artifact)
}

// save writes one new artifact and asks for it to be indexed. Every format
// goes through here, so the storage key, the order of the writes and what a
// failure leaves behind are decided once.
func (s Saver) save(ctx context.Context, artifact NewArtifact) (Saved, error) {
	owner := artifact.Owner
	created, err := s.Rows.Create(ctx, artifact, s.Files, func(artifactID int64) string {
		// The key format every reader of the store expects: a fresh name
		// per file, so an object is written once and never replaced. The
		// extension is the file's own.
		// It is artifacts/{account}/{artifact}/…, the shape an uploaded
		// document's key has (documents/{account}/{document}/…), so the
		// store's objects are grouped by whose they are.
		return "artifacts/" + owner.UserID + "/" + strconv.FormatInt(artifactID, 10) +
			"/primary/" + s.NewID() + path.Ext(artifact.Filename)
	})
	if err != nil {
		return Saved{}, err
	}
	// The artifact exists and opens whether or not it is searchable yet, so
	// a failure to queue the indexing does not fail the save. It is logged:
	// the document shows as pending until something indexes it.
	if err := s.Index(ctx, created.DocumentID, owner.UserID); err != nil {
		s.Logger.LogAttrs(ctx, slog.LevelError, "an artifact was saved and its indexing could not be queued",
			slog.Int64("artifact_id", created.ArtifactID), slog.Int64("document_id", created.DocumentID),
			slog.String("error", err.Error()))
	}
	// Never nil: the page's card reads "files" as a list, and null is not
	// one. An artifact with no file answers an empty list.
	files := []SavedFile{}
	if artifact.HasFile() {
		files = append(files, SavedFile{FileID: created.FileID, Role: "primary", Filename: artifact.Filename, MimeType: artifact.MimeType, SizeBytes: len(artifact.Data)})
	}
	if created.PreviewFileID != 0 {
		files = append(files, SavedFile{FileID: created.PreviewFileID, Role: "preview", Filename: artifact.PreviewFilename, MimeType: pdfMime, SizeBytes: len(artifact.Preview)})
	}
	return Saved{Status: statusSaved, ArtifactID: created.ArtifactID, Generation: 1, Title: artifact.Title, Format: artifact.Format, Files: files}, nil
}

// statusSaved is the status of a save that happened: the member of the
// card's result the page looks for.
const statusSaved = "saved"

// slug makes a title into a file name: lower-case words joined by hyphens.
// It is what a download is called, so it must be safe on every file system
// and still say what the file is. A title with no letters or digits at all
// is named after its format.
func slug(title, format string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
		if b.Len() >= 80 {
			break
		}
	}
	if b.Len() == 0 {
		return format
	}
	return b.String()
}
