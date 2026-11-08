package requestbody

import "strconv"

// TextList reads a `list[str]` field whose default is an empty list.
//
// An absent field is the default; a value that is not a list is refused once;
// and an item that is not a string is refused under its own index, which
// pydantic reports as a further element of the location path — the string "0",
// not the number, because app.py renders every part of a location as text.
func (m *Model) TextList(name string) []string {
	value, present := m.body.Get(name)
	if !present {
		return []string{}
	}
	items, isList := value.([]any)
	if !isList {
		m.reject(NotAList, name)
		return []string{}
	}

	texts := make([]string, 0, len(items))
	for index, item := range items {
		text, isText := item.(string)
		if !isText {
			m.reject(NotAString, name, itemIndex(index))
			continue
		}
		texts = append(texts, text)
	}
	return texts
}

// itemIndex is an item's position as pydantic reports it in a location path:
// the string "0", not the number, because app.py renders every part of a
// location as text.
func itemIndex(index int) string {
	return strconv.Itoa(index)
}
