package celery

import "strings"

// Mapping is a Python dict whose key order is the order it was built in, for a
// task argument that is one.
//
// A Go map cannot carry that order, and sortedKeys — which every other object
// in an envelope goes through — only happens to agree with Python because no
// envelope the worker tier publishes holds an object with more than one key.
// The first one that does is `push_send`, whose single argument is the
// ten-key payload app/push/events.py:_payload builds, and both the body's
// json.dumps and the argsrepr header write a dict in insertion order. Sorting
// it would have put `failed` before `kind` in bytes Celery wrote the other way
// round.
//
// So a call site that passes a dict whose order is observable passes a Mapping
// and not a map.
type Mapping []Entry

// Entry is one key and its value, in place.
type Entry struct {
	Key   string
	Value any
}

// Of builds a Mapping from pairs given in the order Python inserts them.
func Of(entries ...Entry) Mapping { return Mapping(entries) }

func (m Mapping) json() (string, error) {
	object := pyObject{}
	for _, entry := range m {
		encoded, err := pyJSON(entry.Value)
		if err != nil {
			return "", err
		}
		object = object.add(entry.Key, encoded)
	}
	return object.encode(), nil
}

func (m Mapping) repr() (string, error) {
	parts := make([]string, 0, len(m))
	for _, entry := range m {
		encoded, err := pyRepr(entry.Value)
		if err != nil {
			return "", err
		}
		parts = append(parts, pyReprString(entry.Key)+": "+encoded)
	}
	return "{" + strings.Join(parts, ", ") + "}", nil
}
