package requestnumber

import (
	"errors"
	"reflect"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
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
	// "1.0", " 7" and "7 " were in this list until 2026-11-08 and were wrong:
	// the Python tier reads all three (TestParseReadsWhatPydanticReadsFromAString).
	for _, text := range []string{"", "abc", "1e3", "0x10", "٧"} {
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

// The vectors below came out of the running Python tier through the log
// summary's own `hours` parameter, which echoes the value it parsed, and out of
// the `workspace_id` path parameter of a route already handed over. They are
// the reason internal/requestnumber is not a regular expression.
func TestParseReadsWhatPydanticReadsFromAString(t *testing.T) {
	accepted := map[string]int64{
		"1":                  1,
		"+1":                 1,
		"-1":                 -1,
		" 1 ":                1,
		"\t1\n":              1,
		"\r1":                1,
		"\v1":                1,
		"\f1":                1,
		" 1":                 1,
		" 1":                 1,
		"　1":                 1,
		"1 ":                 1,
		"1_0":                10,
		"1_0_0":              100,
		"1_0_0_0":            1000,
		"00_1":               1,
		"0_0":                0,
		"0001":               1,
		"+0":                 0,
		"-0":                 0,
		"1.0":                1,
		"1.00":               1,
		"1.000000":           1,
		"1.0000000000000000": 1,
		"0.0":                0,
		"+1.0":               1,
		"-1.0":               -1,
		"1_000.0":            1000,
		"10_0.00":            100,
	}
	for text, want := range accepted {
		value, failure, err := Parse(text, "query", "hours")
		if failure != nil || err != nil {
			t.Errorf("Parse(%q) refused it; the Python tier reads %d", text, want)
			continue
		}
		if value != want {
			t.Errorf("Parse(%q) = %d, the Python tier reads %d", text, value, want)
		}
	}

	refused := []string{
		"", " ", "_1", "1_", "1__0", "1.5", "1e3", "1.0e3", "1e-3", "0x10",
		"0b1", "0o7", "+_1", "- 1", "1 000", "1,0", "1+1", "--1", "++1",
		"1.0_0", ".0", "1.", "1.0000000000000001", "inf", "nan", "None",
		"true", "٢", "１", "١٢", "1\x001", "1.0.0",
	}
	for _, text := range refused {
		_, failure, err := Parse(text, "query", "hours")
		if failure == nil || err != nil {
			t.Errorf("Parse(%q) did not refuse it; the Python tier answers 422", text)
		}
	}
}

// A value Python holds as an integer and then fails on is not a 422 on either
// tier: it is the query or the arithmetic failing, and a sanitised 500.
func TestParseReportsAnIntegerTooWideAsOutOfRangeRatherThanInvalid(t *testing.T) {
	for _, text := range []string{"9999999999999999999999999", "-9999999999999999999999999", "9_999_999_999_999_999_999_999"} {
		_, failure, err := Parse(text, "path", "workspace_id")
		if failure != nil {
			t.Errorf("Parse(%q) answered 422; the Python tier answers 500", text)
		}
		if !errors.Is(err, ErrOutOfRange) {
			t.Errorf("Parse(%q) error = %v, want ErrOutOfRange", text, err)
		}
	}
}
