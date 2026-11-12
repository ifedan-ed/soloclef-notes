package prompts

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// Requested is a validated PromptCreate (app/schemas/prompts.py).
type Requested struct {
	Name     string
	Prompt   string
	Mode     string
	IsPublic bool
}

// ReadRequest validates the body of a create.
//
// The fields are read in declaration order (name, prompt, mode, is_public),
// because that is the order their failures are reported in whatever order the
// request wrote them, and every field is read even after one has failed: a
// body with three faults is answered with all three.
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
	requested.Name = model.Text("name", nameMinLength, nameMaxLength)
	requested.Prompt = model.Text("prompt", promptMinLength, 0)
	requested.Mode = model.TextMatching("mode", 0, 0, ModePattern)
	// An omission is false and an explicit null is a refusal.
	requested.IsPublic = model.Truth("is_public", false)
	if !model.Valid() {
		return Requested{}, model.Failures(), nil
	}
	return requested, nil, nil
}
