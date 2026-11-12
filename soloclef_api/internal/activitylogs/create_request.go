package activitylogs

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// Requested is a validated new entry.
type Requested struct {
	Level    string
	Status   string
	Message  string
	Source   *string
	Metadata *pythonjson.Object
}

// ReadRequest validates the body of a create. The fields are read in
// declaration order, because that is the order their failures are reported
// in, and every one is read even after one has failed. Nothing in the body
// says whose log it is: the entry is the caller's.
func ReadRequest(contentType string, body []byte) (Requested, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return Requested{}, nil, err
	}
	if failure != nil {
		return Requested{}, []errorenvelope.Failure{*failure}, nil
	}

	model := requestbody.NewModel(object)
	var requested Requested
	requested.Level = model.Choice("level", levels)
	requested.Status = model.Choice("status", statuses)
	requested.Message = model.Text("message", 0, 0)
	requested.Source = model.OptionalText("source", 0)
	requested.Metadata = model.MaybeMapping("log_metadata").Value
	if !model.Valid() {
		return Requested{}, model.Failures(), nil
	}
	return requested, nil, nil
}
