package agentrules

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"

// Build renders one rule: id, thread_id, permission, pattern, action,
// created_at. It says nothing of whose the rule is, because a rule is only
// ever answered to its owner.
//
// created_at is written as every other record of this tier writes a time: a
// `Z`, six fraction digits for a half second and none for a whole one.
func Build(rule Rule) *pythonjson.Object {
	rendered := pythonjson.NewObject()
	rendered.Set("id", rule.ID)
	if rule.ThreadID != nil {
		rendered.Set("thread_id", *rule.ThreadID)
	} else {
		rendered.Set("thread_id", nil)
	}
	rendered.Set("permission", rule.Permission)
	rendered.Set("pattern", rule.Pattern)
	rendered.Set("action", rule.Action)
	rendered.Set("created_at", pythonjson.PydanticDateTime(rule.CreatedAt))
	return rendered
}

// BuildList renders the rules in the order the query returned them.
func BuildList(rules []Rule) []any {
	rendered := make([]any, 0, len(rules))
	for _, rule := range rules {
		rendered = append(rendered, Build(rule))
	}
	return rendered
}
