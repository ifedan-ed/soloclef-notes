package eventstream

import "time"

// DocumentEnteredFolderType is the event a ready document arriving in a
// folder states.
const DocumentEnteredFolderType = "document.entered_folder"

// DocumentEnteredFolder is that event's payload, which an automation's filter
// matches field by field.
//
// Who made the document is not in it: a document is its owner's, and the
// owner is the event's own user_id.
type DocumentEnteredFolder struct {
	DocumentID       int64  `json:"document_id"`
	FolderID         int64  `json:"folder_id"`
	PreviousFolderID *int64 `json:"previous_folder_id"`
	DocumentType     string `json:"document_type"`
	Title            string `json:"title"`
	ConnectorID      *int64 `json:"connector_id"`
}

// DocumentEnteredFolderEvent stamps one such arrival for the document's owner.
func DocumentEnteredFolderEvent(userID string, payload DocumentEnteredFolder, at time.Time) (Event, error) {
	return NewEvent(DocumentEnteredFolderType, payload, userID, at)
}
