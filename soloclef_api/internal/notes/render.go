package notes

import (
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// renderNote is one note as the apps read it. A read sends `content`, the
// whole Markdown; a list sends `preview` instead, the start of the text or
// the passage a search matched.
//
//	id, title, content | preview, origin, notebook, automation_id,
//	thread_id, message_id, [chat_notebook_id,] pinned, pinned_at, sources,
//	index_state, created_at, updated_at
//
// `chat_notebook_id`, sent with a read only, is the notebook whose chat a
// kept answer came from: where the note's "From a chat" opens.
//
// `index_state` is the indexer's: "pending" means the chat does not have
// the newest text yet, which is what an app shows while it catches up.
func renderNote(note Note, whole bool) *pythonjson.Object {
	rendered := pythonjson.NewObject()
	rendered.Set("id", note.ID)
	rendered.Set("title", note.Title)
	if whole {
		rendered.Set("content", note.Text)
	} else {
		rendered.Set("preview", note.Preview)
	}
	rendered.Set("origin", note.Origin)
	rendered.Set("notebook", renderNotebook(note.Notebook))
	rendered.Set("automation_id", numberOrNil(note.AutomationID))
	rendered.Set("thread_id", numberOrNil(note.ThreadID))
	rendered.Set("message_id", numberOrNil(note.MessageID))
	if whole {
		rendered.Set("chat_notebook_id", numberOrNil(note.ChatNotebookID))
	}
	rendered.Set("pinned", note.PinnedAt != nil)
	rendered.Set("pinned_at", momentOrNil(note.PinnedAt))
	sources := make([]any, 0, len(note.Sources))
	for index := range note.Sources {
		sources = append(sources, renderNotebook(&note.Sources[index]))
	}
	rendered.Set("sources", sources)
	rendered.Set("index_state", note.State)
	rendered.Set("created_at", pythonjson.PydanticDateTime(note.CreatedAt))
	rendered.Set("updated_at", pythonjson.PydanticDateTime(note.EditedAt))
	return rendered
}

// renderPage is a list's answer: the page, how many notes match in all,
// and whether there is more after it.
func renderPage(page Page) *pythonjson.Object {
	items := make([]any, 0, len(page.Notes))
	for _, note := range page.Notes {
		items = append(items, renderNote(note, false))
	}
	body := pythonjson.NewObject()
	body.Set("items", items)
	body.Set("total", page.Total)
	body.Set("offset", page.Offset)
	body.Set("limit", page.Limit)
	body.Set("has_more", page.HasMore())
	return body
}

func renderNotebook(notebook *NotebookRef) any {
	if notebook == nil {
		return nil
	}
	rendered := pythonjson.NewObject()
	rendered.Set("id", notebook.ID)
	rendered.Set("title", notebook.Title)
	if notebook.Icon == nil {
		rendered.Set("icon", nil)
	} else {
		rendered.Set("icon", *notebook.Icon)
	}
	return rendered
}

func numberOrNil(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func momentOrNil(value *time.Time) any {
	if value == nil {
		return nil
	}
	return pythonjson.PydanticDateTime(*value)
}
