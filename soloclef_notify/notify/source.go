package notify

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_notify/pushpolicy"
)

// When a source that was indexed is worth a notification at all. A failure
// always is. A success is silent unless it took long enough, or was big
// enough, that the person has probably gone to do something else: a
// document ready in seconds is seen ready in the list it was added from,
// and a notification for it would only be noise.
const (
	// SlowIndexing: two minutes of indexing (fetching, parsing, describing
	// pictures, embedding) is past the point anyone watches a spinner.
	SlowIndexing = 2 * time.Minute
	// LargeSource: a 20 MB file is a book or a long scan, the kind of
	// import a person starts and leaves.
	LargeSource = 20 << 20
)

// How source notifications collapse: everything of one outcome within ten
// minutes is one row ("10 imports failed"), and the push of a new row waits
// a minute for the rest of its batch. An upload of many files is indexed
// a few at a time, so its failures arrive over a minute or two, not at once.
const (
	sourceWindow = 10 * time.Minute
	sourceDelay  = time.Minute
)

// The documents that are not sources a person added: a note they wrote and
// an artifact the Studio made. Indexing either is bookkeeping; a Studio
// output says for itself when it is ready or failed.
var notSources = map[string]bool{"NOTE": true, "ARTIFACT": true}

// SourceOutcome is how one document's indexing ended.
type SourceOutcome struct {
	DocumentID int64
	Failed     bool
	// Reason is what the document's row was told about a failure.
	Reason string
	// Bytes is the size of the file fetched (0 for text with no file);
	// Took is how long the indexing job ran.
	Bytes int64
	Took  time.Duration
}

// The inbox type of a source's row: the type the Status tab already shows
// for documents.
const sourceType = "document_processing"

// SourceIndexed notifies the owner of a source that failed to index, or of
// a large or slow one that is ready (see SlowIndexing and LargeSource). A
// document of a synced connection that succeeds is never reported: a sync
// is not something the person started just now. Everything else is a
// no-op and answers nil.
func (n *Notifier) SourceIndexed(ctx context.Context, outcome SourceOutcome) error {
	if !outcome.Failed && outcome.Took < SlowIndexing && outcome.Bytes < LargeSource {
		return nil
	}
	document, err := New(n.Database).SourceDocument(ctx, outcome.DocumentID)
	if errors.Is(err, pgx.ErrNoRows) {
		// Deleted since: nothing to tell anyone about.
		return nil
	}
	if err != nil {
		return fmt.Errorf("notify: reading document %d: %w", outcome.DocumentID, err)
	}
	if notSources[document.DocumentType] || (!outcome.Failed && document.Synced) {
		return nil
	}
	_, err = n.record(ctx, sourceEvent(outcome, document))
	return err
}

func sourceEvent(outcome SourceOutcome, document SourceDocumentRow) event {
	status, outcomeKey, render := "completed", "ready", readySources
	if outcome.Failed {
		status, outcomeKey, render = "failed", "failed", failedSources
	}
	first := render(1, []string{document.Title})
	path := "/inbox"
	var notebook *int64
	if document.NotebookID != 0 {
		notebook = &document.NotebookID
		path = "/notebook/" + strconv.FormatInt(document.NotebookID, 10) + "?pane=sources"
	}
	return event{
		userID: document.UserID, kind: pushpolicy.Source, typ: sourceType,
		title: first.title, message: first.message,
		push: push{Kind: pushpolicy.Source, Title: first.pushTitle, Path: path},
		metadata: map[string]any{
			"status": status, "processing_stage": status,
			"document_id": outcome.DocumentID, "document_name": document.Title, "document_type": document.DocumentType,
			"error_message": nonEmpty(outcome.Reason), "notebook_id": notebook,
		},
		group: &group{key: "source:" + outcomeKey, window: sourceWindow, delay: sourceDelay, item: document.Title, render: render},
	}
}

// failedSources words a group of failed sources. The file names are the
// inbox's; the push says only how many.
func failedSources(count int, names []string) rendered {
	if count == 1 {
		return rendered{
			title:     clipTitle(names[0]) + " could not be added",
			message:   "It could not be indexed, so the chat cannot read it yet. Open it to see why and try again.",
			pushTitle: "An import failed",
		}
	}
	return rendered{
		title:     fmt.Sprintf("%d imports failed", count),
		message:   listed(count, names) + " could not be indexed.",
		pushTitle: fmt.Sprintf("%d imports failed", count),
	}
}

// readySources words a group of sources that are ready.
func readySources(count int, names []string) rendered {
	if count == 1 {
		return rendered{
			title:     clipTitle(names[0]) + " is ready",
			message:   "It is indexed and the chat can read it.",
			pushTitle: "Your import is ready",
		}
	}
	return rendered{
		title:     fmt.Sprintf("%d sources are ready", count),
		message:   listed(count, names) + " are indexed and the chat can read them.",
		pushTitle: fmt.Sprintf("%d imports are ready", count),
	}
}

// listed names the latest few of count: "a, b and 8 more".
func listed(count int, names []string) string {
	shown := make([]string, len(names))
	for i, name := range names {
		shown[i] = clipTitle(name)
	}
	if rest := count - len(shown); rest > 0 {
		return strings.Join(shown, ", ") + fmt.Sprintf(" and %d more", rest)
	}
	if len(shown) == 1 {
		return shown[0]
	}
	return strings.Join(shown[:len(shown)-1], ", ") + " and " + shown[len(shown)-1]
}

// clipTitle keeps a name short enough for a title (the column holds 200).
func clipTitle(name string) string {
	const most = 120
	name = strings.TrimSpace(name)
	if runes := []rune(name); len(runes) > most {
		return string(runes[:most]) + "…"
	}
	if name == "" {
		return "A source"
	}
	return name
}
