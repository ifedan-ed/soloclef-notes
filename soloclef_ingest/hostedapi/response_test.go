package hostedapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

func TestEachStatusBecomesTheKindTheCallerActsOn(t *testing.T) {
	cases := map[int]parse.Kind{
		401: parse.Unauthorized,
		402: parse.Unauthorized,
		403: parse.Unauthorized,
		413: parse.TooLarge,
		415: parse.UnsupportedFormat,
		422: parse.Rejected,
		404: parse.Rejected,
		429: parse.RateLimited,
		500: parse.Unavailable,
		503: parse.Unavailable,
		302: parse.BadResponse,
	}
	for status, want := range cases {
		response := &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(`{"detail":"why"}`))}
		err := StatusError(parse.Datalab, "the upload", response)
		if err.Kind != want || err.Status != status || err.Parser != parse.Datalab {
			t.Errorf("%d: %+v", status, err)
		}
		if !strings.HasSuffix(err.Message, ": why") || strings.Contains(err.Message, "{") {
			t.Errorf("%d: the service's own words are missing, or the raw reply is there: %s", status, err.Message)
		}
		if !strings.Contains(err.Error(), `{"detail":"why"}`) {
			t.Errorf("%d: the log line lost the reply: %s", status, err.Error())
		}
	}
}

func TestAFailureReasonIsTheServicesSentenceNotItsJSON(t *testing.T) {
	for body, want := range map[string]string{
		// The model proxy's shape, a provider's reply inside: the class
		// names go, the sentence stays.
		`{"error":{"message":"litellm.BadRequestError: OpenAIException - Image could not be decoded.","type":null,"param":null,"code":"400"}}`: "describing photo.png was refused (status 400): Image could not be decoded",
		`{"error":"Invalid API key"}`:                    "describing photo.png was refused (status 400): Invalid API key",
		`{"message":"Too many pages"}`:                   "describing photo.png was refused (status 400): Too many pages",
		`{"error":{"message":"{\"nested\": \"json\"}"}}`: "describing photo.png was refused (status 400)",
		`<html><body>Bad request</body></html>`:          "describing photo.png was refused (status 400)",
		`Bad Request`:                                    "describing photo.png was refused (status 400): Bad Request",
		``:                                               "describing photo.png was refused (status 400)",
	} {
		response := &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(body))}
		if got := StatusError(parse.Vision, "describing photo.png", response).Message; got != want {
			t.Errorf("%s:\n got %q\nwant %q", body, got, want)
		}
	}
	response := &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{"detail":"Down for upkeep."}`))}
	if got := StatusError(parse.Datalab, "the upload", response).Message; got != "the upload failed at the service (status 503): Down for upkeep" {
		t.Errorf("a 5xx: %q", got)
	}
	long := `{"detail":"` + strings.Repeat("word ", 200) + `"}`
	response = &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(long))}
	if got := StatusError(parse.Datalab, "the upload", response).Message; len(got) > mostServiceWords+80 || !strings.HasSuffix(got, "word…") {
		t.Errorf("a long sentence is not cut at a word: %d %q", len(got), got)
	}
}

func TestAnErrorPageIsNotCopiedWholeIntoTheMessage(t *testing.T) {
	response := &http.Response{StatusCode: 502, Body: io.NopCloser(strings.NewReader(strings.Repeat("x", 1<<20)))}
	if err := StatusError(parse.Local, "the parse", response); len(err.Message) > errorBodyLimit+100 {
		t.Fatalf("message is %d bytes", len(err.Message))
	}
}

func TestATransportFailureIsUnavailable(t *testing.T) {
	cause := errors.New("connection refused")
	err := TransportError(context.Background(), parse.Local, "the parse", cause)
	if parse.KindOf(err) != parse.Unavailable || !errors.Is(err, cause) {
		t.Fatalf("err = %v", err)
	}
}

func TestADeadlineIsTimedOutAndACancellationIsTheCallersOwn(t *testing.T) {
	expired, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	<-expired.Done()
	if err := TransportError(expired, parse.Local, "the parse", expired.Err()); parse.KindOf(err) != parse.TimedOut {
		t.Fatalf("deadline: err = %v", err)
	}

	cancelled, stop := context.WithCancel(context.Background())
	stop()
	if err := TransportError(cancelled, parse.Local, "the parse", cancelled.Err()); !errors.Is(err, context.Canceled) || parse.KindOf(err) != "" {
		t.Fatalf("cancel: err = %v", err)
	}
}

func TestDecodeJSONBoundsTheReply(t *testing.T) {
	var value struct{ Markdown string }
	if err := DecodeJSON(parse.Datalab, "the result", strings.NewReader(`{"markdown":"ok"}`), 100, &value); err != nil || value.Markdown != "ok" {
		t.Fatalf("value = %+v, err = %v", value, err)
	}
	big := `{"markdown":"` + strings.Repeat("x", 200) + `"}`
	err := DecodeJSON(parse.Datalab, "the result", strings.NewReader(big), 100, &value)
	if parse.KindOf(err) != parse.BadResponse || !strings.Contains(err.Error(), "larger than 100") {
		t.Fatalf("err = %v", err)
	}
	err = DecodeJSON(parse.Datalab, "the result", strings.NewReader(`<html>`), 100, &value)
	if parse.KindOf(err) != parse.BadResponse {
		t.Fatalf("err = %v", err)
	}
}
