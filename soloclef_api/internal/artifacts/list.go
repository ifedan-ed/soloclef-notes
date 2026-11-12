package artifacts

import (
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Listed is one row of the list's join: the artifact, and the two columns
// of its document the list reads.
type Listed struct {
	ID         int64
	DocumentID int64
	// Title and Status are the document's.
	Title      string
	Format     string
	Generation int64
	// Status is documents.status as the column's text.
	Status   []byte
	ThreadID *int64
	// CreatedAt and UpdatedAt are the artifact's own, not the document's.
	CreatedAt time.Time
	// UpdatedAt is what the list is ordered by, newest first.
	UpdatedAt *time.Time
	// Metadata is artifacts.metadata as the column's text, nil for NULL.
	Metadata []byte
}

// BuildList is the list as the Studio reads it: one item per artifact, in
// the order the query gave.
//
// Every row is rendered. A row whose status or metadata column holds
// something unexpected is listed without what could not be read, rather than
// failing the whole list.
func BuildList(rows []Listed) []any {
	items := make([]any, 0, len(rows))
	for _, row := range rows {
		items = append(items, buildListItem(row))
	}
	return items
}

// buildListItem is one artifact, with the field names the web app's artifact
// list parses.
func buildListItem(row Listed) *pythonjson.Object {
	item := pythonjson.NewObject()
	item.Set("artifact_id", row.ID)
	item.Set("document_id", row.DocumentID)
	item.Set("title", row.Title)
	item.Set("format", row.Format)
	item.Set("generation", row.Generation)
	item.Set("indexing_status", indexingStatus(decodeColumn(row.Status)))
	item.Set("thread_id", optionalNumber(row.ThreadID))
	// ISO 8601 with the offset written out ("+00:00"), which is what the
	// list has always sent for these two.
	item.Set("created_at", pythonjson.ISOFormat(row.CreatedAt))
	if row.UpdatedAt == nil {
		item.Set("updated_at", nil)
	} else {
		item.Set("updated_at", pythonjson.ISOFormat(*row.UpdatedAt))
	}
	// The key is absent, not null, when there is no legacy reference.
	if legacy := legacyRef(decodeColumn(row.Metadata)); legacy != nil {
		item.Set("legacy", legacy)
	}
	return item
}

// indexingStatus is where the artifact's document is in indexing: the
// `state` of its status, and "ready" for a document that names none -- which
// is what a status column that was never written means.
//
// The answer is always a word. A state stored as null or a number used to be
// sent as it was, to a client that switches on a string.
func indexingStatus(status any) string {
	if state, isText := objectOf(status).GetOrNil("state").(string); isText && state != "" {
		return state
	}
	return defaultIndexing
}

// legacyRef is the podcast, video or image an artifact was migrated from,
// kept under `metadata.legacy`: `{"kind": "podcast", "id": 7}`. It is rebuilt
// from those two keys alone, so anything else stored beside them is not
// sent, and it is nil unless the kind is text and the id an integer.
func legacyRef(metadata any) *pythonjson.Object {
	return legacyFrom(objectOf(metadata).GetOrNil("legacy"))
}

// legacyFrom is the same reference built from the value under
// `metadata.legacy` itself, for the manifest, whose statement selects that
// key alone rather than the whole column.
func legacyFrom(value any) *pythonjson.Object {
	legacy, isObject := value.(*pythonjson.Object)
	if !isObject {
		return nil
	}
	kind, isText := legacy.GetOrNil("kind").(string)
	id, isInteger := integer(legacy.GetOrNil("id"))
	// The id must also be positive. Both of the web app's schemas that read
	// this (ArtifactListItemSchema, ArtifactManifestSchema) require a
	// positive integer, and a reference they refuse fails the parse of the
	// *whole* answer: one migrated row with an id of 0 would blank the
	// Studio's list, or leave an artifact that cannot be opened. No row it
	// points at has such an id, so a reference that carries one points at
	// nothing and is left out, as an absent one is.
	if !isText || !isInteger || !positive(id) {
		return nil
	}
	reference := pythonjson.NewObject()
	reference.Set("kind", kind)
	reference.Set("id", id)
	return reference
}

// positive says a JSON integer's digits are a number above zero: no sign,
// and not zero however many digits spell it.
func positive(digits pythonjson.RawInt) bool {
	if len(digits) == 0 || digits[0] == '-' {
		return false
	}
	for _, digit := range []byte(digits) {
		if digit != '0' {
			return true
		}
	}
	return false
}

// optionalNumber is a nullable integer column: null, or the number.
func optionalNumber(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
