package requestbody

import "strings"

// Choice reads a required field typed as a string enum.
//
// pydantic names every member in the refusal, in declaration order, with the
// last joined by "or" — `Input should be 'DEBUG', 'INFO', 'WARNING', 'ERROR' or
// 'CRITICAL'`. A value of any other type gets the same message rather than a
// type complaint, including null, which was checked against the Python tier.
func (m *Model) Choice(name string, options []string) string {
	value, present := m.body.Get(name)
	if !present {
		m.reject(FieldRequired, name)
		return ""
	}
	text, isText := value.(string)
	if isText {
		for _, option := range options {
			if text == option {
				return text
			}
		}
	}
	m.reject(OneOf(options), name)
	return ""
}

// MaybeChoice reads the same field on a partial-update model.
func (m *Model) MaybeChoice(name string, options []string) MaybeText {
	value, present := m.body.Get(name)
	if !present {
		return MaybeText{}
	}
	if value == nil {
		return MaybeText{Present: true}
	}
	text, isText := value.(string)
	if isText {
		for _, option := range options {
			if text == option {
				return MaybeText{Present: true, Value: &text}
			}
		}
	}
	m.reject(OneOf(options), name)
	return MaybeText{Present: true}
}

// OneOf words the refusal as pydantic words it.
func OneOf(options []string) string {
	quoted := make([]string, 0, len(options))
	for _, option := range options {
		quoted = append(quoted, "'"+option+"'")
	}
	switch len(quoted) {
	case 0:
		return "Input should be a valid string"
	case 1:
		return "Input should be " + quoted[0]
	default:
		return "Input should be " + strings.Join(quoted[:len(quoted)-1], ", ") +
			" or " + quoted[len(quoted)-1]
	}
}
