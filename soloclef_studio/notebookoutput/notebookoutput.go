// Package notebookoutput writes the row a notebook's Studio lists an output
// by (notebook_outputs): the one statement that does, shared by the
// Studio's own buttons (soloclef_api/internal/studiogenerate) and by a
// notebook's chat, which lists what it makes there too.
//
// Why the chat lists what it makes: a person who asked a notebook's chat
// for a quiz, a deck or a video looked for it in that notebook's Studio and
// did not find it, because only what the Studio itself started had a row.
// Now an artifact, a video or a podcast made in a notebook's chat gets one
// in the same transaction as the artifact, the job or the podcast, so the
// Studio shows it with its status and offers open, rename and delete as for
// one it made itself.
//
// Only a notebook's chat lists here. A Studio generation runs the same
// specialist under a hidden thread of its own and writes its output row
// itself, before the run, so the caller says which it is: a zero notebook
// lists nothing (artifactsave.Owner.NotebookID).
package notebookoutput

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// maxTitleChars is the Studio's bound on a title (the rename route holds a
// person to the same).
const maxTitleChars = 200

// Place is where something a chat made is listed: the account, its
// notebook, and the chat's thread, which the output remembers so its
// Studio row can lead back to the conversation it was made in. All three
// are the server's, from the turn, never from a model.
type Place struct {
	UserID     string
	NotebookID int64
	ThreadID   int64
}

// The formats an artifact is saved with (the artifacts table's `format`,
// which soloclef_assistant/deliverables names), spelled here because this
// module cannot import that one; deliverables' test holds the two to each
// other.
const (
	formatFlashcards  = "flashcards"
	formatQuiz        = "quiz"
	formatMindMap     = "mindmap"
	formatInfographic = "infographic"
	formatPPTX        = "pptx"
	formatXLSX        = "xlsx"
	formatPDF         = "pdf"
	formatDOCX        = "docx"
	formatHTML        = "html"
)

// KindOf is the Studio's kind for an artifact's format, with the options a
// Studio output of that kind keeps (studiogenerate's Request.Stored), so
// the row reads as the Studio's own: a Word report is a report whose file
// type is docx, an interactive page a report of format "interactive". A
// format that is none of the Studio's kinds answers false: a picture drawn
// in the chat is a picture in the chat, not a Studio output.
func KindOf(format string) (kind string, options map[string]string, ok bool) {
	switch format {
	case formatFlashcards, formatQuiz, formatMindMap, formatInfographic:
		return format, nil, true
	case formatPPTX:
		return "slides", nil, true
	case formatXLSX:
		return "data_table", nil, true
	case formatPDF, formatDOCX:
		return "report", map[string]string{"format": "document", "file_type": format}, true
	case formatHTML:
		return "report", map[string]string{"format": "interactive"}, true
	}
	return "", nil, false
}

// Artifact lists an artifact the chat saved, in the transaction that saved
// it. A format the Studio has no kind for is not listed.
func Artifact(ctx context.Context, db DBTX, at Place, artifactID int64, title, format string) error {
	kind, options, ok := KindOf(format)
	if !ok {
		return nil
	}
	return list(ctx, db, at, kind, title, options, CreateOutputParams{ArtifactID: &artifactID})
}

// Video lists a video the chat queued, by its job, in the transaction that
// queued it: the Studio follows the job to its status and its artifact.
func Video(ctx context.Context, db DBTX, at Place, jobID int64, title string) error {
	return list(ctx, db, at, "video", title, nil, CreateOutputParams{DeliverableJobID: &jobID})
}

// Podcast lists a podcast the chat opened, by its run, in the transaction
// that opened it.
func Podcast(ctx context.Context, db DBTX, at Place, podcastID int64, title string) error {
	return list(ctx, db, at, "audio", title, nil, CreateOutputParams{PodcastID: &podcastID})
}

// list writes the row. A notebook that is not the account's, or that was
// deleted while the turn ran, writes nothing: there is no Studio to list it
// in, and what was made stays in the chat, which is where it was asked for.
// That is the statement's own owner check answering, and the reason a
// missing row is not an error here.
func list(ctx context.Context, db DBTX, at Place, kind, title string, options map[string]string, run CreateOutputParams) error {
	if at.NotebookID == 0 {
		return nil
	}
	if at.UserID == "" || at.ThreadID == 0 {
		return errors.New("notebookoutput: an output must name its account and its thread")
	}
	stored := map[string]string{"kind": kind}
	for field, value := range options {
		stored[field] = value
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		return err
	}
	threadID := at.ThreadID
	run.Kind, run.Title, run.Options = kind, clip(title), encoded
	// What it was made from is the chat, not a choice of sources: none are
	// named, and the Studio counts none.
	run.SourceDocumentIds = []byte("[]")
	run.NotebookID, run.UserID, run.ThreadID = at.NotebookID, at.UserID, &threadID
	_, err = New(db).CreateOutput(ctx, run)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	return err
}

// clip is the title on one line within the Studio's bound, or the kind's
// own name for an artifact saved with none.
func clip(title string) string {
	runes := []rune(strings.Join(strings.Fields(title), " "))
	if len(runes) > maxTitleChars {
		runes = runes[:maxTitleChars]
	}
	if len(runes) == 0 {
		return "Untitled"
	}
	return string(runes)
}
