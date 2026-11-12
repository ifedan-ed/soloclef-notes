package requestbody

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// middlewareHarness is the middleware in front of a route that records whether
// it ran, so a test can see a refusal was given before the route.
func middlewareHarness() (http.Handler, *bool) {
	reached := false
	route := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		reached = true
		response.WriteHeader(http.StatusOK)
	})
	return Middleware{Covers: func(*http.Request) bool { return true }}.Wrap(route), &reached
}

// envelopeOf reads the parts of the error envelope a client acts on.
func envelopeOf(t *testing.T, recorder *httptest.ResponseRecorder) (status int, message string, fields []struct {
	Loc []string `json:"loc"`
	Msg string   `json:"msg"`
}) {
	t.Helper()
	var envelope struct {
		Error struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
			Fields  []struct {
				Loc []string `json:"loc"`
				Msg string   `json:"msg"`
			} `json:"fields"`
		} `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("the answer is not the error envelope: %v: %s", err, recorder.Body.String())
	}
	return envelope.Error.Status, envelope.Error.Message, envelope.Error.Fields
}

// A body that is not JSON is the 422 validation envelope, located at the body,
// and the route never runs: no credential has been looked at yet, and none
// needs to be for a body nobody can read.
func TestMiddlewareRefusesABodyThatIsNotJSON(t *testing.T) {
	for _, body := range []string{"{", `{"name":"x",}`, `{"a":"\ud800"}`} {
		handler, reached := middlewareHarness()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/things", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnprocessableEntity || *reached {
			t.Fatalf("%s: status = %d, route reached = %v; want 422 before the route", body, recorder.Code, *reached)
		}
		status, message, fields := envelopeOf(t, recorder)
		if status != http.StatusUnprocessableEntity || message != "Validation failed: "+JSONDecodeError {
			t.Errorf("%s: envelope status %d, message %q", body, status, message)
		}
		if len(fields) != 1 || len(fields[0].Loc) != 1 || fields[0].Loc[0] != BodyRoot || fields[0].Msg != JSONDecodeError {
			t.Errorf("%s: fields = %+v, want one failure at the body", body, fields)
		}
	}
}

// Bytes that are not UTF-8 are a 400: they are not text, so there is no JSON
// in them to be wrong.
func TestMiddlewareRefusesABodyThatIsNotUTF8(t *testing.T) {
	handler, reached := middlewareHarness()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/things", strings.NewReader("{\"name\":\"\xff\"}"))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest || *reached {
		t.Fatalf("status = %d, route reached = %v; want 400 before the route", recorder.Code, *reached)
	}
	if _, message, _ := envelopeOf(t, recorder); message != UnparseableBody {
		t.Errorf("message = %q, want %q", message, UnparseableBody)
	}
}

// brokenBody is a body that fails part way, as one does when the client goes
// away or a chunked body breaks off.
type brokenBody struct{ sent bool }

func (b *brokenBody) Read(p []byte) (int, error) {
	if b.sent {
		return 0, errors.New("connection reset by peer")
	}
	b.sent = true
	return copy(p, `{"name":`), nil
}

func (b *brokenBody) Close() error { return nil }

// A body that did not arrive whole is a 400, and the route is not given the
// half that did.
func TestMiddlewareRefusesABodyThatDidNotArrive(t *testing.T) {
	handler, reached := middlewareHarness()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/things", nil)
	request.Body = &brokenBody{}
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest || *reached {
		t.Fatalf("status = %d, route reached = %v; want 400 before the route", recorder.Code, *reached)
	}
}

// A body that is JSON but not what the route wants is the route's to refuse,
// after the credential: the middleware passes it on untouched.
func TestMiddlewarePassesOnJSONItCannotJudge(t *testing.T) {
	for _, body := range []string{`{}`, `[]`, `null`, ""} {
		handler, reached := middlewareHarness()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/things", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, request)

		if recorder.Code != http.StatusOK || !*reached {
			t.Errorf("%q: status = %d, route reached = %v; want the route to decide", body, recorder.Code, *reached)
		}
	}
}
