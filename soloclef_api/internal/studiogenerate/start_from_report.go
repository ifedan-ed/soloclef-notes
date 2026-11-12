package studiogenerate

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// This file is the seam the report reading view starts an output through
// (internal/studioviews): a suggestion under a report's section, accepted.
// It is here and not there so that an accepted suggestion is made by the
// very steps a dialog's request is: the same vocabulary check, the same
// idempotency by request id, the same podcast opener, video queuer and
// artifact runner. Only the documents differ.

// FromReport is what an accepted suggestion asks for.
type FromReport struct {
	// Kind is the studio kind; Title, Focus and Language are a dialog's
	// fields of the same names.
	Kind, Title, Focus, Language string
	// RequestID makes a second click on the same suggestion the same
	// output. Letters, digits, '_' and '-' only, as a dialog's is.
	RequestID string
	// DocumentIDs is what the report was made from.
	DocumentIDs []int64
}

// StartFromReport starts the output and answers its id.
//
// It reads the report's own sources, as far as the notebook still holds
// them, because a deck about a report's third section should be drawn from
// what the report was drawn from, not from whatever is ticked at the moment
// of the click. A report all of whose sources have since left the notebook
// has nothing of its own to read, and then the ticked sources are read, as
// for any other output: the person still gets what they asked for, from
// what the notebook now holds.
//
// A request the dialog's route would refuse is refused the same way; Refused
// reads the status and the sentence.
func (r Routes) StartFromReport(ctx context.Context, who Caller, notebookID int64, asked FromReport) (int64, error) {
	body, err := json.Marshal(map[string]string{
		"kind": asked.Kind, "title": asked.Title, "focus": asked.Focus,
		"language": asked.Language, "request_id": asked.RequestID,
	})
	if err != nil {
		return 0, err
	}
	// Through the dialog's own reader, so that every preset the kind has is
	// present with its default and nothing outside the vocabulary starts.
	request, failures := parseRequest(body)
	if len(failures) > 0 {
		return 0, &refusal{http.StatusUnprocessableEntity, failures[0].Message}
	}
	return r.start(ctx, who, notebookID, request, func(ctx context.Context, q *Queries) ([]SourceDocument, error) {
		if len(asked.DocumentIDs) > 0 {
			rows, err := q.DocumentsStillInNotebook(ctx, DocumentsStillInNotebookParams{
				UserID: who.UserID, NotebookID: notebookID, DocumentIds: asked.DocumentIDs, Most: maxSourceDocuments,
			})
			if err != nil {
				return nil, err
			}
			if len(rows) > 0 {
				documents := make([]SourceDocument, len(rows))
				for i, row := range rows {
					documents[i] = SourceDocument{ID: row.ID, Title: row.Title, Text: strings.TrimSpace(row.Body)}
				}
				return documents, nil
			}
		}
		return readSources(ctx, q, who.UserID, notebookID, nil)
	})
}

// Refused reads an error of StartFromReport that is a refusal the person is
// told: its status and its sentence. ok is false for any other error.
func Refused(err error) (status int, message string, ok bool) {
	var refused *refusal
	if errors.As(err, &refused) {
		return refused.status, refused.message, true
	}
	return 0, "", false
}

// CanMake reports whether this deployment can make the kind as its dialog's
// defaults ask for it: a kind built by code needs the sandbox, and a drawn
// one needs the image model. The report view offers only suggestions it
// can then start.
func (r Routes) CanMake(kind string) bool {
	body, err := json.Marshal(map[string]string{"kind": kind})
	if err != nil {
		return false
	}
	request, failures := parseRequest(body)
	return len(failures) == 0 && (r.SandboxEnabled || !needsSandbox(request)) &&
		(r.ImageEnabled || !needsImageModel(request))
}
