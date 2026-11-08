package requestbody

import (
	"strconv"
	"unicode/utf8"
)

// Text reads a required `str` field with pydantic's own length constraints.
//
// A missing field, a null and a number are all refused: pydantic does not read
// a string out of anything but a string. The length is counted in characters
// rather than bytes, because Python's len() of a str counts code points.
func (m *Model) Text(name string, minLength, maxLength int) string {
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
	m.checkLength(name, text, minLength, maxLength)
	return text
}

// OptionalText reads a `str | None` field whose default is None: absent and
// null are both None, and anything that is not a string is refused.
func (m *Model) OptionalText(name string, maxLength int) *string {
	value, present := m.body.Get(name)
	if !present || value == nil {
		return nil
	}
	text, isText := value.(string)
	if !isText {
		m.reject(NotAString, name)
		return nil
	}
	m.checkLength(name, text, 0, maxLength)
	return &text
}

func (m *Model) checkLength(name, text string, minLength, maxLength int) {
	length := utf8.RuneCountInString(text)
	if minLength > 0 && length < minLength {
		m.reject("String should have at least "+characters(minLength), name)
		return
	}
	if maxLength > 0 && length > maxLength {
		m.reject("String should have at most "+characters(maxLength), name)
	}
}

// characters words a length the way pydantic words it, which pluralises: the
// refusal for an empty name reads "at least 1 character".
func characters(count int) string {
	if count == 1 {
		return "1 character"
	}
	return strconv.Itoa(count) + " characters"
}
