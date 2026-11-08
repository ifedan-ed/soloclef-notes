package folders

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// The four bodies of app/schemas/folders.py. Each field is read in the model's
// declaration order, because that is the order FastAPI reports failures in —
// `{"name": [], "parent_id": "x", "workspace_id": "y"}` is answered with all
// three in that order and not the request's.
type (
	// CreateRequested is a validated FolderCreate.
	CreateRequested struct {
		Name        string
		ParentID    requestbody.OptionalNumber
		WorkspaceID int64
		// WorkspaceOutOfRange is a workspace id Python reads and the
		// permission query then fails on, which is a 500 *before* the parent
		// is looked at.
		WorkspaceOutOfRange bool
	}
	// RenameRequested is a validated FolderUpdate: one required name.
	RenameRequested struct{ Name string }
	// MoveRequested is a validated FolderMove.
	MoveRequested struct{ NewParentID requestbody.OptionalNumber }
	// ReorderRequested is a validated FolderReorder: the two neighbours, each
	// absent or null meaning None.
	ReorderRequested struct{ Before, After *string }
)

// ReadCreateRequest validates the body as FolderCreate.
func ReadCreateRequest(contentType string, body []byte) (CreateRequested, []errorenvelope.Failure, error) {
	model, failures, err := readModel(contentType, body)
	if model == nil {
		return CreateRequested{}, failures, err
	}
	var requested CreateRequested
	requested.Name = model.Text("name", 1, MaxNameLength)
	requested.ParentID = model.OptionalNumber("parent_id")
	requested.WorkspaceID = model.Number("workspace_id")
	if !model.Valid() {
		return CreateRequested{}, model.Failures(), nil
	}
	requested.WorkspaceOutOfRange = model.OutOfRange()
	return requested, nil, nil
}

// ReadRenameRequest validates the body as FolderUpdate.
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

// ReadMoveRequest validates the body as FolderMove.
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

// ReadReorderRequest validates the body as FolderReorder.
//
// Both fields are `str | None = None`, with no length and no pattern, so an
// unusable key is not a 422: it reaches fractional_indexing and that raises,
// which is the route's sanitised 500 on both tiers. `""` is one of those.
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

// readModel decodes the body and reports a model-level failure — an absent
// body, a body that is not an object, a content type FastAPI does not read as
// JSON — before any field is looked at. A nil model with no failures and no
// error cannot happen; the three results are the decode's own three outcomes.
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
