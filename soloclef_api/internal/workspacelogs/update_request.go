package workspacelogs

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// Changes is a validated LogUpdate: every field optional, and each carrying
// whether the request mentioned it at all.
//
// The distinction is the route's behaviour. update_log acts on
// model_dump(exclude_unset=True), so a field the body did not mention is not
// written — while an explicit null *is* written, and for level, status and
// message that is a NOT NULL violation and a 500 on both tiers.
type Changes struct {
	Level    requestbody.MaybeText
	Status   requestbody.MaybeText
	Message  requestbody.MaybeText
	Source   requestbody.MaybeText
	Metadata requestbody.MaybeMapping
}

// Mentions reports whether the body named any field at all. A body that named
// none writes nothing: the Python route's session has no changed attribute to
// flush, so it emits no UPDATE and answers with the row as it stands.
func (c Changes) Mentions() bool {
	return c.Level.Present || c.Status.Present || c.Message.Present ||
		c.Source.Present || c.Metadata.Present
}

// ReadChanges validates the body as LogUpdate (app/schemas/logs.py).
func ReadChanges(contentType string, body []byte) (Changes, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return Changes{}, nil, err
	}
	if failure != nil {
		return Changes{}, []errorenvelope.Failure{*failure}, nil
	}

	model := requestbody.NewModel(object)
	var changes Changes
	changes.Level = model.MaybeChoice("level", levels)
	changes.Status = model.MaybeChoice("status", statuses)
	changes.Message = model.MaybeText("message", 0, 0)
	changes.Source = model.MaybeText("source", 0, 0)
	changes.Metadata = model.MaybeMapping("log_metadata")
	if !model.Valid() {
		return Changes{}, model.Failures(), nil
	}
	return changes, nil, nil
}
