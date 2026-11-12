package requestnumber

import (
	"net/url"
	"testing"
)

// Asked of the running Python tier through a route declaring an optional
// integer id: `?id=1&id=2` answered about the **second** one. Starlette builds query_params by writing
// each pair into a dict in order, so the last value of a repeated parameter is
// the one a single-valued annotation reads — and Go's url.Values.Get returns
// the first, which is the divergence this function exists to close.
func TestARepeatedParameterIsReadAsStarletteReadsIt(t *testing.T) {
	for _, test := range []struct {
		query string
		value string
		named bool
	}{
		{"", "", false},
		{"other=1", "", false},
		{"hours=1", "1", true},
		{"hours=", "", true},
		{"hours=1&hours=2", "2", true},
		{"hours=1&hours=2&hours=3", "3", true},
		{"hours=1&hours=", "", true},
		{"hours=&hours=1", "1", true},
	} {
		parsed, err := url.ParseQuery(test.query)
		if err != nil {
			t.Fatal(err)
		}
		value, named := QueryValue(parsed, "hours")
		if named != test.named || value != test.value {
			t.Errorf("%q: got (%q, %v), want (%q, %v)",
				test.query, value, named, test.value, test.named)
		}
	}
}
