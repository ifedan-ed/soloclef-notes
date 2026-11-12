package prompts

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// Changes is a validated PromptUpdate: every field optional, and each carrying
// whether the request mentioned it at all.
//
// The distinction is the route's behaviour three times over. update_prompt
// acts on model_dump(exclude_unset=True), so whether the body *mentioned* a
// field decides whether it is written, whether the version is bumped, and
// whether publishing is refused. An explicit null is a value rather than an
// omission, and writing one into any of these four columns is a NOT NULL
// violation and a 500 on both tiers.
type Changes struct {
	Name     requestbody.MaybeText
	Prompt   requestbody.MaybeText
	Mode     requestbody.MaybeText
	IsPublic requestbody.MaybeTruth
}

// MentionsContent reports whether the body named name, prompt or mode — the
// three fields the Python route calls content, and the only ones that bump the
// version. Mentioning a field with the value it already holds still counts,
// there and here, because the test is on the keys of the dump and not on the
// values.
func (c Changes) MentionsContent() bool {
	return c.Name.Present || c.Prompt.Present || c.Mode.Present
}

// Mentions reports whether the body named any field at all. A body that named
// none writes nothing: the Python route's session has no changed attribute to
// flush, so it emits no UPDATE and answers with the row as it stands.
func (c Changes) Mentions() bool {
	return c.MentionsContent() || c.IsPublic.Present
}

// AsksToPublish is _refuse_public_unless_admin's first argument, which is the
// Python expression `updates.get("is_public") and not prompt.is_public` and
// not the simpler reading of it. Three of its four branches are falsy:
//
//	absent          -> None  -> no refusal
//	explicit null   -> None  -> no refusal, and then a 500 writing NULL
//	false           -> False -> no refusal
//	true, already public  -> False -> no refusal: an owner may keep and edit a
//	                                  prompt that is already published
//	true, not yet public  -> True  -> refused unless the caller may publish
func (c Changes) AsksToPublish(alreadyPublic bool) bool {
	if !c.IsPublic.Present || c.IsPublic.Value == nil || !*c.IsPublic.Value {
		return false
	}
	return !alreadyPublic
}

// ReadChanges validates the body as PromptUpdate (app/schemas/prompts.py).
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
	changes.IsPublic = model.MaybeTruth("is_public")
	if !model.Valid() {
		return Changes{}, model.Failures(), nil
	}
	return changes, nil, nil
}
