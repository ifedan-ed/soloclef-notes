package folders

import "testing"

// Every row below was asked of the running Python tier one at a time: the
// column was set, `PATCH /api/v1/folders/{id}/watched` was sent, and the column
// and `updated_at` were read back. The answer is 200 in all of them, so the
// response says nothing about which happened and only the row does — which is
// why these are vectors and not a reading of the two lines of Python.
func TestWhatTheWatchedFlagWritesAndWhatItLeavesAlone(t *testing.T) {
	for _, each := range []struct {
		why     string
		stored  string
		written string
	}{
		{"a column that is SQL NULL is falsy, so nothing is written", "", ""},
		{"the JSON scalar null is falsy too", "null", ""},
		{"an empty object is falsy — this is the surprising one", "{}", ""},
		{"a list is truthy and is not a dict, so it is left alone", `["a"]`, ""},
		{"a string is truthy and is not a dict", `"watched"`, ""},
		{"a number is truthy and is not a dict", `7`, ""},
		{"a flag that is on is turned off", `{"watched": true}`, `{"watched": false}`},
		{"a flag that is already off is left alone", `{"watched": false}`, ""},
		{"the number zero equals False in Python, so it is left alone",
			`{"watched": 0}`, ""},
		{"and so does the float zero", `{"watched": 0.0}`, ""},
		{"and negative zero", `{"watched": -0.0}`, ""},
		{"an empty list does not equal False, so the flag is written",
			`{"watched": []}`, `{"watched": false}`},
		{"nor does null", `{"watched": null}`, `{"watched": false}`},
		{"nor does the string", `{"watched": "false"}`, `{"watched": false}`},
		{"the number one is written", `{"watched": 1}`, `{"watched": false}`},
		{"an object with other keys keeps them, and the flag is added",
			`{"source": "drive"}`, `{"source": "drive", "watched": false}`},
		{"the flag replaces the one that was there, keys in their stored order",
			`{"source": "drive", "watched": true}`,
			`{"source": "drive", "watched": false}`},
		// The round trip is lossy and that is the Python route's behaviour, not
		// a shortcut here: the dict goes back out through json.dumps, so a
		// number the jsonb column held more precisely than a float64 is
		// rewritten rounded. Measured on the running tier, which stored
		// 0.12345678901234568 for the value below.
		{"a number more precise than a float64 comes back rounded",
			`{"precise": 0.1234567890123456789012345}`,
			`{"precise": 0.12345678901234568, "watched": false}`},
		{"an integer wider than an int64 is kept exactly, because Python holds it as an int",
			`{"huge": 123456789012345678901234567890}`,
			`{"huge": 123456789012345678901234567890, "watched": false}`},
		{"a non-ASCII value is escaped, as json.dumps escapes it",
			"{\"who\": \"\u00e9\"}", "{\"who\": \"\\u00e9\", \"watched\": false}"},
	} {
		cleared, write, err := watchedCleared([]byte(each.stored))
		if err != nil {
			t.Errorf("%s: %v", each.why, err)
			continue
		}
		if each.written == "" {
			if write {
				t.Errorf("%s: wrote %s, and the Python route writes nothing", each.why, cleared)
			}
			continue
		}
		if !write {
			t.Errorf("%s: wrote nothing, and the Python route writes %s", each.why, each.written)
			continue
		}
		if string(cleared) != each.written {
			t.Errorf("%s: wrote %s, the Python route writes %s", each.why, cleared, each.written)
		}
	}
}

func TestAColumnThatIsNotJSONIsAFailureRatherThanAGuess(t *testing.T) {
	if _, _, err := watchedCleared([]byte(`{"watched":`)); err == nil {
		t.Error("a column that does not decode was read as an answer")
	}
}
