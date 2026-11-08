package workspacelogs

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// Requested is a validated LogCreate (app/schemas/logs.py).
type Requested struct {
	Level       string
	Status      string
	Message     string
	Source      *string
	Metadata    *pythonjson.Object
	WorkspaceID int64
}

// ReadRequest validates the body as LogCreate. The fields are read in the
// model's declaration order because that is the order FastAPI reports their
// failures in, and every one is read even after one has failed.
//
// The second result of the error triple is the one case that is not a
// validation failure: a workspace id Python reads and the query then fails on,
// which is a 500 after the body has validated rather than a 422.
func ReadRequest(contentType string, body []byte) (Requested, []errorenvelope.Failure, bool, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return Requested{}, nil, false, err
	}
	if failure != nil {
		return Requested{}, []errorenvelope.Failure{*failure}, false, nil
	}

	model := requestbody.NewModel(object)
	var requested Requested
	requested.Level = model.Choice("level", levels)
	requested.Status = model.Choice("status", statuses)
	requested.Message = model.Text("message", 0, 0)
	requested.Source = model.OptionalText("source", 0)
	requested.Metadata = model.MaybeMapping("log_metadata").Value
	requested.WorkspaceID = model.Number("workspace_id")
	if !model.Valid() {
		return Requested{}, model.Failures(), false, nil
	}
	return requested, nil, model.OutOfRange(), nil
}
