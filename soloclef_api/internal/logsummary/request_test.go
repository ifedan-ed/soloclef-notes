package logsummary

import (
	"errors"
	"net/url"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

func TestReadParametersDefaultsTheWindowToTwentyFourHours(t *testing.T) {
	parsed, failures, err := readParameters("12", url.Values{})
	if err != nil || len(failures) > 0 {
		t.Fatalf("got %v, %v", failures, err)
	}
	if parsed.WorkspaceID != 12 || parsed.Hours != 24 {
		t.Errorf("got %+v", parsed)
	}
}

func TestReadParametersRefusesAnEmptyWindowAsFastAPIDoes(t *testing.T) {
	// `?hours=` is a value FastAPI tries to parse and cannot; `?` absent is
	// the default. The two must not be confused.
	_, failures, err := readParameters("12", url.Values{"hours": {""}})
	if err != nil {
		t.Fatalf("Since: %v", err)
	}
	if len(failures) != 1 || failures[0].Message != errorenvelope.IntegerExpected {
		t.Fatalf("got %+v", failures)
	}
	if got := failures[0].Location; len(got) != 2 || got[0] != "query" || got[1] != "hours" {
		t.Errorf("location: got %v", got)
	}
}

func TestReadParametersReportsThePathBeforeTheQuery(t *testing.T) {
	_, failures, err := readParameters("abc", url.Values{"hours": {"xyz"}})
	if err != nil {
		t.Fatalf("readParameters: %v", err)
	}
	if len(failures) != 2 {
		t.Fatalf("got %d failures, want 2", len(failures))
	}
	if failures[0].Location[1] != "workspace_id" || failures[1].Location[1] != "hours" {
		t.Errorf("the failures are in the wrong order: %+v", failures)
	}
}

func TestReadParametersAcceptsSignsAndRefusesEverythingElse(t *testing.T) {
	// "1.0" and " 12" were refused here until 2026-11-08 and should not have
	// been: pydantic reads both, and internal/requestnumber now does
	// (TestParseReadsWhatPydanticReadsFromAString).
	for _, text := range []string{"+12", "1.0", " 12", "1_2"} {
		if _, failures, _ := readParameters(text, url.Values{"hours": {"-5"}}); len(failures) > 0 {
			t.Errorf("%q: got %+v", text, failures)
		}
	}
	for _, text := range []string{"1e3", "0x10", "١٢"} {
		if _, failures, _ := readParameters(text, url.Values{}); len(failures) != 1 {
			t.Errorf("%q: want one failure, got %+v", text, failures)
		}
	}
}

func TestReadParametersReportsANumberNoRequestCanUse(t *testing.T) {
	// The Python route takes these as ints and then fails: the query raises on
	// a workspace id no column can hold, and timedelta raises on the window.
	// Both are its sanitised 500, not a validation error.
	for name, call := range map[string]func() (parameters, []errorenvelope.Failure, error){
		"a workspace id past int64": func() (parameters, []errorenvelope.Failure, error) {
			return readParameters("99999999999999999999", url.Values{})
		},
		"a window past int64": func() (parameters, []errorenvelope.Failure, error) {
			return readParameters("1", url.Values{"hours": {"99999999999999999999"}})
		},
	} {
		if _, _, err := call(); !errors.Is(err, requestnumber.ErrOutOfRange) {
			t.Errorf("%s: got %v, want requestnumber.ErrOutOfRange", name, err)
		}
	}
}
