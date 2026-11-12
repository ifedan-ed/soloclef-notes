package documentlibrary

import (
	"encoding/json"
	"time"
)

// document is a document as the clients read it (the web's `document`
// schema and the MCP tools' renderers). There is no owner field: a document
// is the caller's or it is not answered.
type document struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Type     string `json:"document_type"`
	Metadata any    `json:"document_metadata"`
	// Content is the whole text for one document and empty in a list, where
	// Preview carries its beginning.
	Content     string     `json:"content"`
	Preview     string     `json:"content_preview"`
	ContentHash string     `json:"content_hash"`
	Identity    *string    `json:"unique_identifier_hash"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   *time.Time `json:"updated_at"`
	FolderID    *int64     `json:"folder_id"`
	Status      status     `json:"status"`
}

// status is a document's state and, for a failed one, why.
type status struct {
	State  string  `json:"state"`
	Reason *string `json:"reason"`
}

// page is a page of documents.
type page struct {
	Items    []document `json:"items"`
	Total    int64      `json:"total"`
	Page     int64      `json:"page"`
	PageSize int64      `json:"page_size"`
	HasMore  bool       `json:"has_more"`
}

// metadataOf is the metadata column as the object the clients expect. The
// column may be NULL, and one odd row (a list, a string) must not fail a
// whole page: anything that is not an object is answered as an empty one.
func metadataOf(column []byte) any {
	var object map[string]json.RawMessage
	if len(column) == 0 || json.Unmarshal(column, &object) != nil || object == nil {
		return map[string]any{}
	}
	return object
}

// statusOf reads the status column, which the schema holds to an object
// naming a state.
func statusOf(column []byte) status {
	var read status
	_ = json.Unmarshal(column, &read)
	if read.State == "" {
		read.State = "ready"
	}
	return read
}

// utc is a timestamp in UTC: the driver hands back the session's zone, and
// a client should not be told the server's.
func utc(moment *time.Time) *time.Time {
	if moment == nil {
		return nil
	}
	shifted := moment.UTC()
	return &shifted
}

// firstRunes is the first n characters of text, never half of one.
func firstRunes(text string, n int) string {
	seen := 0
	for index := range text {
		if seen == n {
			return text[:index]
		}
		seen++
	}
	return text
}

func fromPageRow(row DocumentPageRow) document {
	return document{
		ID: row.ID, Title: row.Title, Type: row.DocumentType, Metadata: metadataOf(row.DocumentMetadata),
		Preview: row.Preview, ContentHash: row.ContentHash, Identity: row.UniqueIdentifierHash,
		CreatedAt: row.CreatedAt.UTC(), UpdatedAt: utc(row.UpdatedAt), FolderID: row.FolderID, Status: statusOf(row.Status),
	}
}

func fromRow(row DocumentOfOwnerRow) document {
	return document{
		ID: row.ID, Title: row.Title, Type: row.DocumentType, Metadata: metadataOf(row.DocumentMetadata),
		Content: row.Body, Preview: firstRunes(row.Body, previewChars),
		ContentHash: row.ContentHash, Identity: row.UniqueIdentifierHash,
		CreatedAt: row.CreatedAt.UTC(), UpdatedAt: utc(row.UpdatedAt), FolderID: row.FolderID, Status: statusOf(row.Status),
	}
}
