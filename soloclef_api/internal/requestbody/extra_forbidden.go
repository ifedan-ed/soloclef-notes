package requestbody

// ExtraForbidden is pydantic's message for a key a model with
// `extra="forbid"` did not declare.
const ExtraForbidden = "Extra inputs are not permitted"

// RefuseExtras is `model_config = ConfigDict(extra="forbid")`.
//
// Every model this tier had read until now leaves pydantic's default of
// "ignore", so an undeclared field was a 200 and the matrices say so. `app/
// schemas/users.py:UserUpdate` is the first that forbids them, and it forbids
// them *for a reason a client can hit*: that model deliberately does not
// declare `email` or `password`, and the docstring says the refusal is how a
// client that sends either learns it was not changed. Ignoring them would have
// answered 200 to a request that changed no address.
//
// Two things about the order were read off pydantic-core rather than reasoned
// about, with `{"display_name": 5, "nope": 1, "zz": 2}` asked of the model
// itself:
//
//   - the **declared** fields' failures come first, in declaration order, and
//     the extras after them. So this is called after every declared field has
//     been read, not before.
//   - the extras are reported in the order the **request** wrote them, not
//     sorted, which is why it walks the decoded object's keys
//     (internal/pythonjson.Object keeps insertion order).
//
// Each extra is its own failure under its own name, so a body with two of them
// is answered with two.
func (m *Model) RefuseExtras(declared ...string) {
	for _, key := range m.body.Keys() {
		if !names(declared, key) {
			m.reject(ExtraForbidden, key)
		}
	}
}

func names(declared []string, key string) bool {
	for _, name := range declared {
		if name == key {
			return true
		}
	}
	return false
}
