package requestbody

import "testing"

// TestAnEmptyBodyIsFieldRequiredWhateverTheHeader pins the order Decode tests
// the body and the media type in.
//
// Measured against the running Python app on 2026-11-11, driving
// a route that takes a required body, with the caller stubbed so the body
// error is reached instead of the 401:
//
//	Content-Type        body    answer
//	application/json    ""      Field required
//	text/plain          ""      Field required
//	(no header)         ""      Field required
//	application/json    null    Field required
//	text/plain          hi      Input should be a valid dictionary ...
//	application/json    []      Input should be a valid dictionary ...
//
// So an ABSENT body is the model being absent whatever the header, and the
// mapping complaint belongs only to a body that is present and is not an
// object. Decode tested the media type first and so answered the mapping
// complaint for an empty body with a non-JSON header -- on every route of this
// tier that takes a required body. This test is here so that cannot come back.
func TestAnEmptyBodyIsFieldRequiredWhateverTheHeader(t *testing.T) {
	for _, c := range []struct {
		name        string
		contentType string
		body        string
		want        string
	}{
		{"empty as json", "application/json", "", FieldRequired},
		{"empty as text", "text/plain", "", FieldRequired},
		{"empty with no header", "", "", FieldRequired},
		{"json null", "application/json", "null", FieldRequired},
		{"present but not an object, as text", "text/plain", "hi", NotAnObject},
		{"present but not an object, as json", "application/json", "[]", NotAnObject},
	} {
		t.Run(c.name, func(t *testing.T) {
			object, failure, err := Decode(c.contentType, []byte(c.body))
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if object != nil {
				t.Fatalf("got an object, want the failure %q", c.want)
			}
			if failure == nil {
				t.Fatalf("got no failure, want %q", c.want)
			}
			if got := failure.Message; got != c.want {
				t.Errorf("message is %q, Python answers %q", got, c.want)
			}
		})
	}
}
