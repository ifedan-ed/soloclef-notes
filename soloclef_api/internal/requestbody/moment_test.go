package requestbody

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// testdata/moment_python.json holds pydantic's own answer for 136 shapes of a
// `datetime | None` body field, recorded against the Python version while it
// still ran. This replays every one of them through the reader.
//
// It is the check that matters here. The forms a datetime can take are not
// something to get right by reading speedate's documentation: "2026-03-04
// 05:06:07" is valid and "2026-3-4T05:06:07Z" is not, "20260304" is a unix
// timestamp in August 1970, a cap of 20,000,000,001 is milliseconds while
// 20,000,000,000 is seconds, and a bad *hour* is reported as trailing
// characters after a date.
func TestPydanticsOwnDatetimes(t *testing.T) {
	replayRecordedMoments(t, "moment_python.json")
}

// testdata/moment_fuzz.json is the same comparison over 708 inputs nobody
// chose by hand: every single-character deletion and substitution of one valid
// datetime, every truncation of it, random strings over its alphabet, and
// random numeric literals as both strings and numbers. Two of pydantic's
// refusals were only ever produced by this table -- "invalid character in
// month" and "invalid character in day" -- and the reader answered the
// range complaint for both until it ran.
func TestPydanticsOwnDatetimesUnderFuzz(t *testing.T) {
	replayRecordedMoments(t, "moment_fuzz.json")
}

func replayRecordedMoments(t *testing.T, fixture string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", fixture))
	if err != nil {
		t.Fatalf("reading the recorded answers: %v", err)
	}
	var recorded struct {
		Cases []struct {
			Raw       string `json:"raw"`
			OK        bool   `json:"ok"`
			Mentioned bool   `json:"mentioned"`
			Value     string `json:"value"`
			UTCOffset *int   `json:"utcoffset"`
			Naive     bool   `json:"naive"`
			Errors    []struct {
				Type string   `json:"type"`
				Msg  string   `json:"msg"`
				Loc  []string `json:"loc"`
			} `json:"errors"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatalf("parsing the recorded answers: %v", err)
	}
	if len(recorded.Cases) == 0 {
		t.Fatal("the recorded table is empty, so it proves nothing")
	}

	read, refused, absent, skipped := 0, 0, 0, 0
	for _, c := range recorded.Cases {
		if len(c.Raw) > 7 && c.Raw[:7] == "ABSENT:" {
			// The field was not in the body at all.
			change := readMomentField(t, c.Raw[7:])
			if change.Present {
				t.Errorf("%s: the field is reported as mentioned", c.Raw)
			}
			absent++
			continue
		}

		if opaqueToPydantic(c.Raw) {
			// The one class this reader does not match: see momentOpaqueDigits
			// in moment.go and TestTheOneClassPydanticAnswersOpaquely below.
			skipped++
			continue
		}

		body := `{"expires_at": ` + c.Raw + `}`
		change := readMomentFieldWithFailures(t, body)
		if !change.Present {
			t.Errorf("%s: the field is not reported as mentioned", c.Raw)
			continue
		}

		switch {
		case c.OK && c.Value == "":
			// An explicit null.
			if change.Value != nil {
				t.Errorf("%s: read %v, Python read None", c.Raw, change.Value)
			}
			if len(change.Failures) > 0 {
				t.Errorf("%s: refused a null: %v", c.Raw, change.Failures)
			}
			read++
		case c.OK:
			if len(change.Failures) > 0 {
				t.Errorf("%s: refused with %q; Python read %s",
					c.Raw, change.Failures[0].Message, c.Value)
				continue
			}
			if change.Value == nil {
				t.Errorf("%s: read nothing; Python read %s", c.Raw, c.Value)
				continue
			}
			if got := isoLike(*change.Value, change.Naive); got != c.Value {
				t.Errorf("%s: read %s, Python read %s", c.Raw, got, c.Value)
			}
			if change.Naive != c.Naive {
				t.Errorf("%s: naive = %v, Python said %v", c.Raw, change.Naive, c.Naive)
			}
			read++
		default:
			if len(change.Failures) == 0 {
				t.Errorf("%s: read %v; Python refused with %q",
					c.Raw, change.Value, c.Errors[0].Msg)
				continue
			}
			if got := change.Failures[0].Message; got != c.Errors[0].Msg {
				t.Errorf("%s: refused with\n  %q\nPython refused with\n  %q",
					c.Raw, got, c.Errors[0].Msg)
			}
			if location := change.Failures[0].Location; len(location) != 2 ||
				location[0] != BodyRoot || location[1] != "expires_at" {
				t.Errorf("%s: the failure is at %v, want [%s expires_at]",
					c.Raw, location, BodyRoot)
			}
			refused++
		}
	}
	t.Logf("replayed %d recorded answers: %d read, %d refused, %d absent, "+
		"%d skipped as opaque", len(recorded.Cases), read, refused, absent, skipped)
	if read == 0 || refused == 0 {
		t.Error("a table with no reads or no refusals proves little")
	}
}

type momentOutcome struct {
	MaybeMoment
	Failures []failureLike
}

type failureLike struct {
	Location []string
	Message  string
}

func readMomentField(t *testing.T, body string) MaybeMoment {
	t.Helper()
	return readMomentFieldWithFailures(t, body).MaybeMoment
}

func readMomentFieldWithFailures(t *testing.T, body string) momentOutcome {
	t.Helper()
	object, failure, err := Decode("application/json", []byte(body))
	if err != nil {
		t.Fatalf("Decode(%s): %v", body, err)
	}
	if failure != nil {
		t.Fatalf("Decode(%s) refused the body itself: %+v", body, *failure)
	}
	model := NewModel(object)
	moment := model.MaybeMoment("expires_at")
	outcome := momentOutcome{MaybeMoment: moment}
	for _, found := range model.Failures() {
		outcome.Failures = append(outcome.Failures, failureLike{
			Location: found.Location, Message: found.Message,
		})
	}
	return outcome
}

// isoLike renders a moment the way datetime.isoformat() renders it, which is
// how the recorded answers were written: microsecond precision only when
// there is a fraction, and the offset spelled +HH:MM -- or nothing at all for
// a naive datetime.
func isoLike(moment time.Time, naive bool) string {
	layout := "2006-01-02T15:04:05"
	if moment.Nanosecond() != 0 {
		layout = "2006-01-02T15:04:05.000000"
	}
	rendered := moment.Format(layout)
	if naive {
		return rendered
	}
	return rendered + moment.Format("-07:00")
}

// opaqueToPydantic is the one class of input this reader does not match: a
// numeric string carrying a dot whose whole part has thirteen or more
// significant digits. momentOpaqueDigits says what pydantic does with those
// and why this reader does not follow.
func opaqueToPydantic(raw string) bool {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		// A JSON number, not a string: those agree.
		return false
	}
	text := raw[1 : len(raw)-1]
	if text != "" && (text[0] == '+' || text[0] == '-') {
		text = text[1:]
	}
	dot := -1
	for i := 0; i < len(text); i++ {
		switch {
		case text[i] >= '0' && text[i] <= '9':
		case text[i] == '.' && dot < 0:
			dot = i
		default:
			return false
		}
	}
	if dot < 0 {
		return false
	}
	digits := 0
	for i := 0; i < dot; i++ {
		if digits > 0 || text[i] != '0' {
			digits++
		}
	}
	return digits >= 13
}

// TestTheOneClassPydanticAnswersOpaquely names the divergence rather than
// leaving it to be discovered. Both inputs are pydantic's own answers,
// recorded: it reads a date in 2061 for the first and 1981 for the second,
// which no divisor of the digits explains, and this reader refuses both for
// their range -- which is what pydantic itself answers for the same digits
// without the dot.
func TestTheOneClassPydanticAnswersOpaquely(t *testing.T) {
	for _, text := range []string{"+2874861264401073.121", "374341560324439.27896"} {
		_, _, failure := ReadMoment(text)
		if failure == "" {
			t.Errorf("%q was read; this reader is expected to refuse it", text)
			continue
		}
		if failure != momentPrefix+"dates after 9999 are not supported as unix timestamps" {
			t.Errorf("%q was refused with %q, want the range refusal", text, failure)
		}
	}
	// The same digits without the dot are a range refusal on both tiers, which
	// is what makes the refusal the right side to err on.
	if _, _, failure := ReadMoment("2874861264401073"); failure == "" {
		t.Error("the same digits without a dot were read")
	}
}
