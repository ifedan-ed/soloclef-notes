package accesstokens

import (
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// BuildList renders the list route's list[PATRead] in PATRead's field order and
// in the order the query returned the rows, which the query itself pins.
func BuildList(tokens []Token) []any {
	rendered := make([]any, 0, len(tokens))
	for _, token := range tokens {
		entry := pythonjson.NewObject()
		entry.Set("id", token.ID)
		entry.Set("label", token.Label)
		entry.Set("prefix", token.Prefix)
		entry.Set("expires_at", momentOrNil(token.ExpiresAt))
		entry.Set("last_used_at", momentOrNil(token.LastUsedAt))
		entry.Set("created_at", pythonjson.PydanticDateTime(token.CreatedAt))
		rendered = append(rendered, entry)
	}
	return rendered
}

func momentOrNil(value *time.Time) any {
	if value == nil {
		return nil
	}
	return pythonjson.PydanticDateTime(*value)
}
