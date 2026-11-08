package requestbody

import (
	"errors"
	"testing"
)

func TestDecodeHandsBackABodyCarryingALoneSurrogate(t *testing.T) {
	unpaired := []string{
		`{"a":"\ud800"}`,
		`{"a":"\udc00"}`,
		`{"a":"\ud800x"}`,
		`{"a":"\ud800\ud800"}`,
		`{"a":"\ud800A"}`,
		`{"\ud800":1}`,
		`{"a":"x\\\ud800"}`,
	}
	for _, body := range unpaired {
		if _, _, err := Decode("application/json", []byte(body)); !errors.Is(err, ErrUndecodable) {
			t.Errorf("%s: err = %v, want ErrUndecodable", body, err)
		}
	}

	// A valid pair, an escaped backslash before a "u" that is therefore not an
	// escape at all, and a literal replacement character are all ordinary
	// bodies.
	fine := []string{
		`{"a":"😀"}`,
		`{"a":"\\ud800"}`,
		`{"a":"\\\\ud800"}`,
		`{"a":"A"}`,
		"{\"a\":\"�\"}",
		`{"a":"\\u"}`,
	}
	for _, body := range fine {
		if _, _, err := Decode("application/json", []byte(body)); err != nil {
			t.Errorf("%s: err = %v, want none", body, err)
		}
	}
}
