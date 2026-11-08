package requestbody

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"

// MaybeMapping is a `dict[str, Any] | None` field of a model.
type MaybeMapping struct {
	Present bool
	// Value is nil for an explicit null, and a non-nil object otherwise —
	// including an empty one, which is a different write.
	Value *pythonjson.Object
}

// MaybeMapping reads a `dict[str, Any] | None` field whose default is None.
//
// Its refusal is not the model-level one: a field typed dict is "Input should
// be a valid dictionary", while a *body* that is not an object is "Input should
// be a valid dictionary or object to extract fields from". Both were read off
// the Python tier.
func (m *Model) MaybeMapping(name string) MaybeMapping {
	value, present := m.body.Get(name)
	if !present {
		return MaybeMapping{}
	}
	if value == nil {
		return MaybeMapping{Present: true}
	}
	object, isObject := value.(*pythonjson.Object)
	if !isObject {
		m.reject(NotADictionary, name)
		return MaybeMapping{Present: true}
	}
	return MaybeMapping{Present: true, Value: object}
}
