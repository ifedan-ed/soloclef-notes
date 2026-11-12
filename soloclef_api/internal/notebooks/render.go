package notebooks

import (
	"context"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// A datetime is written RFC 3339 in UTC with a "Z", the microseconds only
// when there are some, and the keys are in the order the apps' schemas
// declare them (internal/pythonjson).

// notebookRead is a notebook as the apps read one.
func notebookRead(notebook Notebook, counts sourceTotals, titlePending bool) *pythonjson.Object {
	read := pythonjson.NewObject()
	read.Set("id", notebook.ID)
	read.Set("title", notebook.Title)
	read.Set("icon", optionalText(notebook.Icon))
	read.Set("created_at", pythonjson.PydanticDateTime(notebook.CreatedAt))
	read.Set("updated_at", pythonjson.PydanticDateTime(notebook.UpdatedAt))
	read.Set("source_count", counts.total)
	read.Set("enabled_source_count", counts.enabled)
	// Each of these three columns is NOT NULL with a CHECK naming its
	// values, so the fallback can only be reached by an empty string the
	// CHECK forbids; it keeps the answer inside the values the apps switch
	// on if a CHECK is ever dropped.
	read.Set("chat_goal", orDefault(notebook.ChatGoal, "default"))
	read.Set("chat_instructions", optionalText(notebook.ChatInstructions))
	read.Set("response_length", orDefault(notebook.ResponseLength, "default"))
	read.Set("featured", notebook.FeaturedAt != nil)
	read.Set("title_source", orDefault(notebook.TitleSource, "user"))
	read.Set("title_pending", titlePending)
	return read
}

// described is what the list and every single-notebook answer do with the
// rows they have: the source counts, then which notebooks are about to name
// themselves -- two statements at most, and none at all for no notebooks.
// Whether each is pinned came with its row. The clock is read once.
func (r Routes) described(ctx context.Context, userID string, notebooks []Notebook) ([]any, error) {
	ids := make([]int64, 0, len(notebooks))
	for _, notebook := range notebooks {
		ids = append(ids, notebook.ID)
	}
	counts, err := countSources(ctx, r.Database, userID, ids)
	if err != nil {
		return nil, err
	}
	pending, err := namingPending(ctx, r.Database, userID, notebooks, r.now())
	if err != nil {
		return nil, err
	}
	reads := make([]any, 0, len(notebooks))
	for _, notebook := range notebooks {
		reads = append(reads, notebookRead(notebook, counts[notebook.ID], pending[notebook.ID]))
	}
	return reads, nil
}

func optionalText(text *string) any {
	if text == nil {
		return nil
	}
	return *text
}

// orDefault is the value, or the fallback for an empty one.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
