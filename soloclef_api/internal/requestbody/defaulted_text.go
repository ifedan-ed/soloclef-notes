package requestbody

// TextOr reads a `str` field that carries a **default**, which is a third shape
// beside Text's required one and OptionalText's `str | None`.
//
// `AgentPermissionRuleCreate.pattern` is the first of them:
//
//	pattern: str = Field("*", min_length=1, max_length=255)
//
// The type is `str` and not `str | None`, so the three inputs are three
// different answers and only the first is the default:
//
//	absent  -> the fallback, and no length check, because pydantic does not
//	           validate a default it supplied
//	null    -> "Input should be a valid string", as for any `str` field
//	""      -> "String should have at least 1 character"
//
// Reading it as OptionalText would make an explicit null the fallback, which
// is a 201 where the Python tier answers 422.
func (m *Model) TextOr(name, fallback string, minLength, maxLength int) string {
	value, present := m.body.Get(name)
	if !present {
		return fallback
	}
	text, isText := value.(string)
	if !isText {
		m.reject(NotAString, name)
		return fallback
	}
	m.checkLength(name, text, minLength, maxLength)
	return text
}
