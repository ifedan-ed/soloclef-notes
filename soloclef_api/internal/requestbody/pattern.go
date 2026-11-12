package requestbody

import "regexp"

// TextMatching reads a required `str` field carrying pydantic's `pattern=`
// constraint, as app/schemas/prompts.py declares it for a prompt's mode.
//
// Three things about it were read off the running tier rather than assumed
// (the vectors are in pattern_test.go):
//
//   - a value that is not a string is NotAString, not the pattern's message:
//     the type is checked first, so `mode: null` and `mode: 7` are both
//     "Input should be a valid string";
//   - the length constraints are checked **before** the pattern, both of them,
//     so a value that is too short and also does not match names its length;
//   - the message names the pattern exactly as it was written, quoted with
//     apostrophes: `String should match pattern '^(transform|explore)$'`.
//
// The constraint is applied with Go's regexp. That is a second engine —
// pydantic-core compiles the pattern with Rust's `regex` crate — and the two
// agree on the only patterns this app declares, which are anchored
// alternations of literals: both are RE2-shaped, both read `^` and `$` as the
// ends of the whole text rather than of a line, and neither has Python's `re`
// quirk of letting `$` match before a trailing newline (checked: a mode of
// "transform\n" is refused by both). A pattern using a feature the two
// engines spell differently — a lookaround, a backreference, a named class —
// must be compared against the Python tier before it is read here.
func (m *Model) TextMatching(name string, minLength, maxLength int, pattern *regexp.Regexp) string {
	value, present := m.body.Get(name)
	if !present {
		m.reject(FieldRequired, name)
		return ""
	}
	text, isText := value.(string)
	if !isText {
		m.reject(NotAString, name)
		return ""
	}
	if !m.checkLength(name, text, minLength, maxLength) {
		return text
	}
	m.checkPattern(name, text, pattern)
	return text
}

// MaybeTextMatching reads the same field on a partial-update model: a `str |
// None` whose string branch carries the pattern. A null skips the pattern as
// it skips the length, because it is the None branch of the union.
func (m *Model) MaybeTextMatching(name string, minLength, maxLength int, pattern *regexp.Regexp) MaybeText {
	value, present := m.body.Get(name)
	if !present {
		return MaybeText{}
	}
	if value == nil {
		return MaybeText{Present: true}
	}
	text, isText := value.(string)
	if !isText {
		m.reject(NotAString, name)
		return MaybeText{Present: true}
	}
	if m.checkLength(name, text, minLength, maxLength) {
		m.checkPattern(name, text, pattern)
	}
	return MaybeText{Present: true, Value: &text}
}

func (m *Model) checkPattern(name, text string, pattern *regexp.Regexp) {
	if pattern == nil || pattern.MatchString(text) {
		return
	}
	m.reject(PatternMismatch(pattern), name)
}

// PatternMismatch words the refusal as pydantic words it, naming the pattern
// as it was written on the model.
func PatternMismatch(pattern *regexp.Regexp) string {
	return "String should match pattern '" + pattern.String() + "'"
}
