package notebooks

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythontext"
)

// seedTemplate is one report template as it ships.
type seedTemplate struct {
	key         string
	format      string
	title       string
	description string
	prompt      string
}

// seedTemplates is the report templates every account starts with, in the
// order every answer lists them. They are data, not UI strings: the dialog
// shows whatever is here or the account's saved edit of it, and the prompt
// is what a generation is given.
var seedTemplates = []seedTemplate{
	{
		key:         "learning_overview",
		format:      "interactive",
		title:       "Learning Overview",
		description: "Summarizes key concepts to help you learn the material.",
		prompt: "Build a learning overview: start with the big picture, then explain " +
			"each key concept in its own section with a plain-language " +
			"explanation, an example and a short self-check question the reader " +
			"can reveal. End with a recap of the main takeaways.",
	},
	{
		key:         "briefing_doc",
		format:      "document",
		title:       "Briefing Doc",
		description: "Overview of the sources featuring key insights and quotes.",
		prompt: "Write a briefing document: an executive summary, the main themes " +
			"with the most important facts and short direct quotes from the " +
			"sources, and a closing section on open questions or implications.",
	},
	{
		key:         "study_guide",
		format:      "document",
		title:       "Study Guide",
		description: "Short-answer quiz, essay questions and a glossary of key terms.",
		prompt: "Write a study guide: a short-answer quiz with an answer key, a few " +
			"essay-format questions without answers, and a glossary of key " +
			"terms with concise definitions.",
	},
	{
		key:         "blog_post",
		format:      "document",
		title:       "Blog Post",
		description: "An engaging article that makes the sources easy to read.",
		prompt: "Write an engaging, well-structured blog post that explains the most " +
			"interesting ideas in the sources for a general audience, with a " +
			"clear headline and subheadings.",
	},
}

// seedFor is seed_for: the seeded template with this key, or nil. The key is
// compared as it was sent -- no case folding, no trimming -- so "BLOG_POST"
// names nothing.
func seedFor(key string) *seedTemplate {
	for index := range seedTemplates {
		if seedTemplates[index].key == key {
			return &seedTemplates[index]
		}
	}
	return nil
}

// shownTemplates is every seed, each with the account's own edit laid over
// it. A row whose key is no longer seeded is not shown.
func shownTemplates(ctx context.Context, db DBTX, userID string) ([]any, error) {
	rows, err := New(db).TemplatesOfOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	edits := make(map[string]TemplatesOfOwnerRow, len(rows))
	for _, row := range rows {
		edits[row.TemplateKey] = row
	}
	templates := make([]any, 0, len(seedTemplates))
	for _, seed := range seedTemplates {
		title, description, prompt := seed.title, seed.description, seed.prompt
		edit, customized := edits[seed.key]
		if customized {
			// `row.title if row else seed.title`: the row's value whatever it
			// is, an empty description included.
			title, description, prompt = edit.Title, edit.Description, edit.Prompt
		}
		// ReportTemplateRead, in the order it declares its fields.
		read := pythonjson.NewObject()
		read.Set("key", seed.key)
		read.Set("format", seed.format)
		read.Set("title", title)
		read.Set("description", description)
		read.Set("prompt", prompt)
		read.Set("customized", customized)
		templates = append(templates, read)
	}
	return templates, nil
}

// storedEdit is what save_template stores for a request, which is not what
// the request sent:
//
//	row.title = (title or "").strip() or seed.title
//	row.description = (description or "").strip() if description is not None else seed.description
//	row.prompt = prompt.strip() or seed.prompt
//
// The three lines differ on purpose. A blank title and a blank prompt each
// fall back to the seed's; a blank description is stored as the empty
// string, and only an absent one falls back -- so saving a template without
// naming its description *replaces* an edited description with the seed's.
func storedEdit(seed *seedTemplate, edit templateEdit) (title, description, prompt string) {
	title = seed.title
	if edit.Title != nil {
		if stripped := pythontext.Strip(*edit.Title); stripped != "" {
			title = stripped
		}
	}
	description = seed.description
	if edit.Description != nil {
		description = pythontext.Strip(*edit.Description)
	}
	prompt = pythontext.Strip(edit.Prompt)
	if prompt == "" {
		prompt = seed.prompt
	}
	return title, description, prompt
}
