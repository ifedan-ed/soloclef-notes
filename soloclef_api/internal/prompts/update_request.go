package prompts

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// Changes is a validated PromptUpdate: every field optional, and each carrying
// whether the request mentioned it at all.
//
// The distinction decides two things: whether a column is written, and
// whether the version is bumped. An explicit null is a value rather than an
// omission, and writing one into any of these three columns is a NOT NULL
// violation and a 500.
type Changes struct {
	Name   requestbody.MaybeText
	Prompt requestbody.MaybeText
	Mode   requestbody.MaybeText
}

// Mentions reports whether the body named name, prompt or mode. Mentioning a
// field with the value it already holds still counts, because the test is on
// the keys the body sent and not on their values. A body that named none
// writes nothing and keeps the version: an old client's lone is_public is
// such a body, since prompts are no longer shared.
func (c Changes) Mentions() bool {
	return c.Name.Present || c.Prompt.Present || c.Mode.Present
}

// ReadChanges validates the body as PromptUpdate (app/schemas/prompts.py).
//
// The fields are read in declaration order (name, prompt, mode), because that
// is the order their failures are reported in whatever order the request
// wrote them.
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
	changes.Name = model.MaybeText("name", nameMinLength, nameMaxLength)
	changes.Prompt = model.MaybeText("prompt", promptMinLength, 0)
	changes.Mode = model.MaybeTextMatching("mode", 0, 0, ModePattern)
	if !model.Valid() {
		return Changes{}, model.Failures(), nil
	}
	return changes, nil, nil
}
