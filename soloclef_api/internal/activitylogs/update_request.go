package activitylogs

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// Changes is a validated LogUpdate: every field optional, and each carrying
// whether the request mentioned it at all.
//
// The distinction is the route's behaviour: a field the body did not mention
// is not written, while an explicit null is, which clears source and
// log_metadata and is refused for the three columns that cannot be empty.
type Changes struct {
	Level    requestbody.MaybeText
	Status   requestbody.MaybeText
	Message  requestbody.MaybeText
	Source   requestbody.MaybeText
	Metadata requestbody.MaybeMapping
}

// Mentions reports whether the body named any field at all. A body that named
// none writes nothing and is answered with the row as it stands.
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
	failures := model.Failures()
	// level, status and message are NOT NULL. A null for one of them is the
	// client's mistake and is told so, where it used to reach the database
	// and come back as a 500.
	for _, field := range []struct {
		name  string
		value requestbody.MaybeText
	}{{"level", changes.Level}, {"status", changes.Status}, {"message", changes.Message}} {
		if sent, _ := object.Get(field.name); field.value.Present && field.value.Value == nil && sent == nil {
			failures = append(failures, errorenvelope.Failure{
				Location: []string{requestbody.BodyRoot, field.name},
				Message:  requestbody.NotAString,
			})
		}
	}
	if len(failures) > 0 {
		return Changes{}, failures, nil
	}
	return changes, nil, nil
}
