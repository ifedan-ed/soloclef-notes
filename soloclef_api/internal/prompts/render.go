package prompts

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"

// Build renders one prompt as PromptRead (app/schemas/prompts.py).
//
// The key order is the model's field order: id, name, prompt, mode,
// is_public, version, created_at.
//
// created_at is pydantic's own spelling rather than isoformat's, because every
// route of this slice but the delete declared a response_model: a `Z` where
// isoformat writes `+00:00`, six fraction digits for a half second and **none**
// at all for a whole one (testdata/library.json pins both).
func Build(prompt Prompt) *pythonjson.Object {
	rendered := pythonjson.NewObject()
	rendered.Set("id", prompt.ID)
	rendered.Set("name", prompt.Name)
	rendered.Set("prompt", prompt.Prompt)
	rendered.Set("mode", prompt.Mode)
	// Nothing is shared since 2026-11-13, so always false: the phone bundles
	// of 2026-11-14 require the key and refuse a prompt without it.
	// installed-builds: prompts-is-public
	rendered.Set("is_public", false)
	rendered.Set("version", prompt.Version)
	rendered.Set("created_at", pythonjson.PydanticDateTime(prompt.CreatedAt))
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

// deleted is delete_prompt's `{"success": True}`.
func deleted() *pythonjson.Object {
	rendered := pythonjson.NewObject()
	rendered.Set(deletedKey, true)
	return rendered
}
