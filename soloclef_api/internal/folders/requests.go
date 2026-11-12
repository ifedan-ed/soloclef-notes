package folders

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// The six bodies. Each field is read in a fixed order, which is the order a
// body with several fields wrong has its failures reported in.
//
// No body names an account: the folders written are the caller's. A body
// that still carries a field these do not read is not refused for it; the
// field is not read.
//
// An id too large for any id column (an OptionalNumber that is OutOfRange)
// is not a validation failure: it is a number, and it names nothing, so the
// route answers what it answers for a folder that is not there.
type (
	// CreateRequested is a validated create: the name, and the parent to
	// make the folder under, or none for the top level.
	CreateRequested struct {
		Name     string
		ParentID requestbody.OptionalNumber
	}
	// RenameRequested is a validated rename: one required name.
	RenameRequested struct{ Name string }
	// MoveRequested is a validated move: the new parent, or none for the
	// top level.
	MoveRequested struct{ NewParentID requestbody.OptionalNumber }
	// ReorderRequested is a validated reorder: the positions of the two
	// folders to sit between, each absent or null at an end of the list.
	ReorderRequested struct{ Before, After *string }
	// DocumentMoveRequested is a validated document move: the folder to
	// file it in, or none to take it out of every folder.
	DocumentMoveRequested struct{ FolderID requestbody.OptionalNumber }
	// BulkMoveRequested is the same for several documents.
	BulkMoveRequested struct {
		DocumentIDs []int64
		FolderID    requestbody.OptionalNumber
	}
)

// ReadCreateRequest validates a create's body.
func ReadCreateRequest(contentType string, body []byte) (CreateRequested, []errorenvelope.Failure, error) {
	model, failures, err := readModel(contentType, body)
	if model == nil {
		return CreateRequested{}, failures, err
	}
	var requested CreateRequested
	requested.Name = model.Text("name", 1, MaxNameLength)
	requested.ParentID = model.OptionalNumber("parent_id")
	if !model.Valid() {
		return CreateRequested{}, model.Failures(), nil
	}
	return requested, nil, nil
}

// ReadRenameRequest validates a rename's body.
func ReadRenameRequest(contentType string, body []byte) (RenameRequested, []errorenvelope.Failure, error) {
	model, failures, err := readModel(contentType, body)
	if model == nil {
		return RenameRequested{}, failures, err
	}
	requested := RenameRequested{Name: model.Text("name", 1, MaxNameLength)}
	if !model.Valid() {
		return RenameRequested{}, model.Failures(), nil
	}
	return requested, nil, nil
}

// ReadMoveRequest validates a folder move's body.
func ReadMoveRequest(contentType string, body []byte) (MoveRequested, []errorenvelope.Failure, error) {
	model, failures, err := readModel(contentType, body)
	if model == nil {
		return MoveRequested{}, failures, err
	}
	requested := MoveRequested{NewParentID: model.OptionalNumber("new_parent_id")}
	if !model.Valid() {
		return MoveRequested{}, model.Failures(), nil
	}
	return requested, nil, nil
}

// ReadReorderRequest validates a reorder's body.
//
// Both fields are free text here. Whether the two are positions a folder
// can be placed between is the key generator's question, and the route
// answers 400 when it says no (reorder_route.go).
func ReadReorderRequest(contentType string, body []byte) (ReorderRequested, []errorenvelope.Failure, error) {
	model, failures, err := readModel(contentType, body)
	if model == nil {
		return ReorderRequested{}, failures, err
	}
	before := model.OptionalText("before_position", 0)
	after := model.OptionalText("after_position", 0)
	if !model.Valid() {
		return ReorderRequested{}, model.Failures(), nil
	}
	return ReorderRequested{Before: before, After: after}, nil, nil
}

// readModel decodes the body and reports a failure of the whole body (it is
// absent, it is not an object, its content type is not JSON) before any
// field is looked at. A nil model with no failures and no error cannot
// happen; the three results are the decode's own three outcomes.
func readModel(contentType string, body []byte) (*requestbody.Model, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return nil, nil, err
	}
	if failure != nil {
		return nil, []errorenvelope.Failure{*failure}, nil
	}
	return requestbody.NewModel(object), nil, nil
}

// ReadDocumentMoveRequest validates a document move's body.
func ReadDocumentMoveRequest(contentType string, body []byte) (DocumentMoveRequested, []errorenvelope.Failure, error) {
	model, failures, err := readModel(contentType, body)
	if model == nil {
		return DocumentMoveRequested{}, failures, err
	}
	requested := DocumentMoveRequested{FolderID: model.OptionalNumber("folder_id")}
	if !model.Valid() {
		return DocumentMoveRequested{}, model.Failures(), nil
	}
	return requested, nil, nil
}

// ReadBulkMoveRequest validates a bulk move's body. An id in the list too
// large for any id column is read as 0, which names no document, exactly
// as an id nobody was ever given names none.
func ReadBulkMoveRequest(contentType string, body []byte) (BulkMoveRequested, []errorenvelope.Failure, error) {
	model, failures, err := readModel(contentType, body)
	if model == nil {
		return BulkMoveRequested{}, failures, err
	}
	var requested BulkMoveRequested
	// No limit on the list's length: the read is one statement whatever
	// the count, and the body's size is already capped.
	requested.DocumentIDs = model.NumberList("document_ids", 0, 0)
	requested.FolderID = model.OptionalNumber("folder_id")
	if !model.Valid() {
		return BulkMoveRequested{}, model.Failures(), nil
	}
	return requested, nil, nil
}
