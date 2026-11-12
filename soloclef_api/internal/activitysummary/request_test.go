package activitysummary

import (
	"errors"
	"net/url"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

func TestTheWindowDefaultsToTwentyFourHours(t *testing.T) {
	hours, failures, err := readHours(url.Values{})
	if err != nil || len(failures) > 0 || hours != 24 {
		t.Fatalf("got %d, %v, %v", hours, failures, err)
	}
}

// `?hours=` is a value that cannot be read; no parameter at all is the
// default. The two must not be confused.
func TestAnEmptyWindowIsRefused(t *testing.T) {
	_, failures, err := readHours(url.Values{"hours": {""}})
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 1 || failures[0].Message != errorenvelope.IntegerExpected {
		t.Fatalf("got %+v", failures)
	}
	if got := failures[0].Location; len(got) != 2 || got[0] != "query" || got[1] != "hours" {
		t.Errorf("location: got %v", got)
	}
}

func TestTheWindowReadsSignsAndRefusesWhatIsNotAWholeNumber(t *testing.T) {
	for _, text := range []string{"+12", "1.0", " 12", "-5"} {
		if _, failures, _ := readHours(url.Values{"hours": {text}}); len(failures) > 0 {
			t.Errorf("%q: got %+v", text, failures)
		}
	}
	for _, text := range []string{"1e3", "0x10", "xyz"} {
		if _, failures, _ := readHours(url.Values{"hours": {text}}); len(failures) != 1 {
			t.Errorf("%q: want one failure, got %+v", text, failures)
		}
	}
}

// A window no request can use is the route's sanitised 500 and not a
// validation error: the number was a number.
func TestAWindowPastInt64IsOutOfRange(t *testing.T) {
	if _, _, err := readHours(url.Values{"hours": {"99999999999999999999"}}); !errors.Is(err, requestnumber.ErrOutOfRange) {
		t.Errorf("got %v, want requestnumber.ErrOutOfRange", err)
	}
}

// `?hours=1&hours=2` is the window the last value names.
func TestTheLastOfARepeatedWindowWins(t *testing.T) {
	hours, failures, err := readHours(url.Values{"hours": {"1", "2"}})
	if err != nil || len(failures) > 0 || hours != 2 {
		t.Fatalf("got %d, %v, %v", hours, failures, err)
	}
}
