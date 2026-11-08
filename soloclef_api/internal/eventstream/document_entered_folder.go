package eventstream

import "time"

// DocumentEnteredFolderType is app/event_bus/events/document_entered_folder.py's
// EVENT_TYPE.
const DocumentEnteredFolderType = "document.entered_folder"

// DocumentEnteredFolder is the payload payload_if_entered_folder builds, field
// for field and in its order.
//
// `is_move` is deliberately absent: the pydantic model derives it, but the dict
// that module publishes does not carry it, and a filter matches the published
// dict. Adding it here would give a Go-published event a field a
// Python-published one does not have.
type DocumentEnteredFolder struct {
	DocumentID       int64   `json:"document_id"`
	FolderID         int64   `json:"folder_id"`
	PreviousFolderID *int64  `json:"previous_folder_id"`
	DocumentType     string  `json:"document_type"`
	Title            string  `json:"title"`
	ConnectorID      *int64  `json:"connector_id"`
	CreatedByID      *string `json:"created_by_id"`
}

// DocumentEnteredFolderEvent stamps one such arrival.
func DocumentEnteredFolderEvent(workspaceID int64, payload DocumentEnteredFolder, at time.Time) (Event, error) {
	return NewEvent(DocumentEnteredFolderType, payload, workspaceID, at)
}
