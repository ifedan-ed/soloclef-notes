package requestnumber

import (
	"errors"
	"reflect"
	"testing"

	"git.danvics.com/danvics/surfsense/soloclef_api/internal/errorenvelope"
)

func TestParseAcceptsWhatPythonAcceptsAsAnIntLiteral(t *testing.T) {
	for text, want := range map[string]int64{
		"0":                    0,
		"7":                    7,
		"+7":                   7,
		"-7":                   -7,
		"007":                  7,
		"9223372036854775807":  9223372036854775807,
		"-9223372036854775808": -9223372036854775808,
		// Past int32, so the query must be the thing that fails, not the parse.
		"3000000000": 3000000000,
	} {
		got, failure, err := Parse(text, "path", "workspace_id")
		if err != nil || failure != nil {
			t.Errorf("%q: got %v, %v", text, failure, err)
			continue
		}
		if got != want {
			t.Errorf("%q: got %d, want %d", text, got, want)
		}
	}
}

func TestParseReportsAValueThatIsNotAWholeNumberAsPydanticDoes(t *testing.T) {
	for _, text := range []string{"", "abc", "1.0", "1e3", " 7", "7 ", "0x10", "٧"} {
		_, failure, err := Parse(text, "query", "hours")
		if err != nil {
			t.Errorf("%q: got an error, want a failure: %v", text, err)
			continue
		}
		want := &errorenvelope.Failure{
			Location: []string{"query", "hours"},
			Message:  errorenvelope.IntegerExpected,
		}
		if !reflect.DeepEqual(failure, want) {
			t.Errorf("%q: got %+v, want %+v", text, failure, want)
		}
	}
}

func TestParseReportsANumberPastInt64AsOutOfRange(t *testing.T) {
	// Python parses it and the route then fails on it, which is a 500 there and
	// must be a 500 here.
	for _, text := range []string{"99999999999999999999", "-99999999999999999999"} {
		if _, _, err := Parse(text, "path", "workspace_id"); !errors.Is(err, ErrOutOfRange) {
			t.Errorf("%q: got %v, want ErrOutOfRange", text, err)
		}
	}
}
