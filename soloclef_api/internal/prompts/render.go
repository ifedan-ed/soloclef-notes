package prompts

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"

// Build renders one prompt as PromptRead (app/schemas/prompts.py).
//
// The key order is the model's field order, read off the declaration: id,
// name, prompt, mode, is_public, version, created_at. There is
// no mixin to reverse here, unlike LogRead, because PromptRead declares all
// eight itself.
//
// created_at is pydantic's own spelling rather than isoformat's, because every
// route of this slice but the delete declares a response_model: a `Z` where
// isoformat writes `+00:00`, six fraction digits for a half second and **none**
// at all for a whole one. Both spellings are in the captured answers
// (testdata/library.json), which is the only reason to trust either.
func Build(prompt Prompt) *pythonjson.Object {
	rendered := pythonjson.NewObject()
	rendered.Set("id", prompt.ID)
	rendered.Set("name", prompt.Name)
	rendered.Set("prompt", prompt.Prompt)
	rendered.Set("mode", prompt.Mode)
	rendered.Set("is_public", prompt.IsPublic)
	rendered.Set("version", prompt.Version)
	rendered.Set("created_at", pythonjson.PydanticDateTime(prompt.CreatedAt))
	return rendered
}

// BuildPublic renders one prompt as PublicPromptRead, which is PromptRead with
// the author appended. A pydantic subclass's new field comes after the ones it
// inherits, which is where the Python route's model_dump-and-splat puts it too
// — read off PublicPromptRead.model_fields rather than reasoned about.
func BuildPublic(prompt Prompt) *pythonjson.Object {
	rendered := Build(prompt)
	rendered.Set("author_name", textOrNil(prompt.AuthorName))
	return rendered
}

// BuildList renders list[PromptRead] in the order the query returned the rows,
// which that query pins with a tie-break (queries.sql).
func BuildList(prompts []Prompt) []any {
	rendered := make([]any, 0, len(prompts))
	for _, prompt := range prompts {
		rendered = append(rendered, Build(prompt))
	}
	return rendered
}

// BuildPublicList renders list[PublicPromptRead].
func BuildPublicList(prompts []Prompt) []any {
	rendered := make([]any, 0, len(prompts))
	for _, prompt := range prompts {
		rendered = append(rendered, BuildPublic(prompt))
	}
	return rendered
}

// deleted is delete_prompt's `{"success": True}`.
func deleted() *pythonjson.Object {
	rendered := pythonjson.NewObject()
	rendered.Set(deletedKey, true)
	return rendered
}

func textOrNil(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
