package knowledgebase

import (
	"context"
	"fmt"
	"strings"
)

// tree is an account's folders, read once for one tool call, so that every
// path a result shows comes from one consistent read and costs no query of
// its own.
type tree struct {
	byID     map[int64]AccountFoldersRow
	children map[int64][]AccountFoldersRow
	top      []AccountFoldersRow
}

func readTree(ctx context.Context, q *Queries, userID string) (tree, error) {
	rows, err := q.AccountFolders(ctx, userID)
	if err != nil {
		return tree{}, fmt.Errorf("reading the folders: %w", err)
	}
	t := tree{byID: make(map[int64]AccountFoldersRow, len(rows)), children: map[int64][]AccountFoldersRow{}}
	for _, row := range rows {
		t.byID[row.ID] = row
		if row.ParentID == nil {
			t.top = append(t.top, row)
		} else {
			t.children[*row.ParentID] = append(t.children[*row.ParentID], row)
		}
	}
	return t, nil
}

// path is a folder's place as a person reads it, "/Pharmacology/Cardio", or
// "/" for no folder. The walk is bounded by the number of folders, so a
// parent link that ever pointed in a circle ends rather than spins.
func (t tree) path(folderID *int64) string {
	if folderID == nil {
		return "/"
	}
	var names []string
	for id, steps := *folderID, 0; steps <= len(t.byID); steps++ {
		row, found := t.byID[id]
		if !found {
			break
		}
		names = append(names, row.Name)
		if row.ParentID == nil {
			break
		}
		id = *row.ParentID
	}
	for i, j := 0, len(names)-1; i < j; i, j = i+1, j-1 {
		names[i], names[j] = names[j], names[i]
	}
	return "/" + strings.Join(names, "/")
}

// kindOf is a document type as the model is told it.
func kindOf(documentType string) string {
	switch documentType {
	case "NOTE":
		return "note"
	case "FILE", "LOCAL_FOLDER_FILE":
		return "uploaded file"
	case "GOOGLE_DRIVE_FILE":
		return "Google Drive file"
	case "CRAWLED_URL":
		return "saved web page"
	case "YOUTUBE_VIDEO":
		return "YouTube video"
	case "ARTIFACT":
		return "generated artifact"
	case "EXTENSION":
		return "saved page"
	}
	return strings.ToLower(documentType)
}

// aKind is a document type with its article, for a sentence.
func aKind(documentType string) string {
	kind := kindOf(documentType)
	switch {
	case kind == "":
		return "a document"
	case strings.ContainsRune("aeiou", rune(kind[0])):
		return "an " + kind
	}
	return "a " + kind
}

// stateNote is what a document's indexing state adds to its line: nothing
// when it is searchable.
func stateNote(state string) string {
	switch state {
	case "ready":
		return ""
	case "failed":
		return ", indexing failed"
	}
	return ", being indexed"
}

// documentLine is one document as a listing shows it.
func documentLine(id int64, title, documentType, state string) string {
	return fmt.Sprintf("- [document %d] %s (%s%s)", id, title, kindOf(documentType), stateNote(state))
}
