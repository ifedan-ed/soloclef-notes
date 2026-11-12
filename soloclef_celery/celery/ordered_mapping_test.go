package celery

import "testing"

// The vectors are what CPython prints for the same dict, taken from the
// interpreter rather than written from a reading of it:
//
//	>>> import json
//	>>> d = {"kind": "mention", "user_id": None, "failed": False, "path": "/inbox"}
//	>>> json.dumps(d)
//	'{"kind": "mention", "user_id": null, "failed": false, "path": "/inbox"}'
//	>>> repr(d)
//	"{'kind': 'mention', 'user_id': None, 'failed': False, 'path': '/inbox'}"
//
// Note that both keep the insertion order and neither sorts, which is the whole
// reason this type exists: sorted, the first key would be "failed".
func TestAMappingKeepsTheOrderItWasBuiltIn(t *testing.T) {
	payload := Of(
		Entry{"kind", "mention"},
		Entry{"user_id", nil},
		Entry{"failed", false},
		Entry{"path", "/inbox"},
	)

	gotJSON, err := pyJSON(payload)
	if err != nil {
		t.Fatalf("pyJSON: %v", err)
	}
	wantJSON := `{"kind": "mention", "user_id": null, "failed": false, "path": "/inbox"}`
	if gotJSON != wantJSON {
		t.Errorf("pyJSON = %s, want %s", gotJSON, wantJSON)
	}

	gotRepr, err := pyRepr(payload)
	if err != nil {
		t.Fatalf("pyRepr: %v", err)
	}
	wantRepr := `{'kind': 'mention', 'user_id': None, 'failed': False, 'path': '/inbox'}`
	if gotRepr != wantRepr {
		t.Errorf("pyRepr = %s, want %s", gotRepr, wantRepr)
	}
}

// An empty dict is `{}` either way, and a nested one is spelled by the same
// two functions.
func TestAMappingNestsAndEmptiesAsPythonDoes(t *testing.T) {
	empty := Of()
	if got, err := pyJSON(empty); err != nil || got != "{}" {
		t.Errorf("pyJSON(empty) = %s, %v", got, err)
	}
	if got, err := pyRepr(empty); err != nil || got != "{}" {
		t.Errorf("pyRepr(empty) = %s, %v", got, err)
	}

	nested := Of(Entry{"outer", Of(Entry{"b", 2}, Entry{"a", 1})})
	if got, err := pyJSON(nested); err != nil || got != `{"outer": {"b": 2, "a": 1}}` {
		t.Errorf("pyJSON(nested) = %s, %v", got, err)
	}
	if got, err := pyRepr(nested); err != nil || got != `{'outer': {'b': 2, 'a': 1}}` {
		t.Errorf("pyRepr(nested) = %s, %v", got, err)
	}
}
