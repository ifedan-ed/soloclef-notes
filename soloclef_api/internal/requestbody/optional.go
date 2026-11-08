package requestbody

// A partial-update model's fields are read differently from a create model's:
// the Python routes act on `model_dump(exclude_unset=True)`, so whether the
// request *mentioned* a field decides whether it is written at all, and an
// explicit JSON null is a value rather than an omission. The two are not
// interchangeable — `{}` leaves a role alone, while `{"name": null}` writes
// NULL into a NOT NULL column and is a 500 on both tiers.

// MaybeText is a `str | None` field of a partial-update model: whether the
// request mentioned it, and the value it carried.
type MaybeText struct {
	Present bool
	// Value is nil for an explicit null, which pydantic accepts for the union
	// and which the route then writes.
	Value *string
}

// MaybeTextList is a `list[str] | None` field of a partial-update model.
type MaybeTextList struct {
	Present bool
	// Value is nil for an explicit null; an empty list is a non-nil empty
	// slice, because the two are a different write.
	Value []string
}

// MaybeTruth is a `bool | None` field of a partial-update model.
type MaybeTruth struct {
	Present bool
	// Value is nil for an explicit null.
	Value *bool
}

// MaybeText reads an optional string with pydantic's length constraints. A null
// skips the length check, as the None branch of the union does.
func (m *Model) MaybeText(name string, minLength, maxLength int) MaybeText {
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
	m.checkLength(name, text, minLength, maxLength)
	return MaybeText{Present: true, Value: &text}
}

// MaybeTextList reads an optional list of strings. A bad item is refused under
// its own index exactly as TextList refuses one.
func (m *Model) MaybeTextList(name string) MaybeTextList {
	value, present := m.body.Get(name)
	if !present {
		return MaybeTextList{}
	}
	if value == nil {
		return MaybeTextList{Present: true}
	}
	items, isList := value.([]any)
	if !isList {
		m.reject(NotAList, name)
		return MaybeTextList{Present: true}
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
	return MaybeTextList{Present: true, Value: texts}
}

// MaybeTruth reads an optional boolean with the same lax coercion Truth uses,
// so `"yes"` is true here as well.
func (m *Model) MaybeTruth(name string) MaybeTruth {
	value, present := m.body.Get(name)
	if !present {
		return MaybeTruth{}
	}
	if value == nil {
		return MaybeTruth{Present: true}
	}
	truth, readable := m.readTruth(name, value)
	if !readable {
		return MaybeTruth{Present: true}
	}
	return MaybeTruth{Present: true, Value: &truth}
}
