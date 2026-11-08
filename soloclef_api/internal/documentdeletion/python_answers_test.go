package documentdeletion

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/capturecompare"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/celeryqueue"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

const captures = "testdata/python_answers.json"

// The captures are what the Python route answered to fourteen requests, taken
// by asking both tiers each of them against the same seeded database
// (custom/docs/go-api-tier.md, "How the two writes were proved"). The
// comparison was made there; this keeps it.
//
// The enqueued message is the part to fear: a response that matches while the
// message differs is a route that looks ported and has stopped deleting
// anything.
func TestTheEnqueuedMessageIsTheOnePythonPublished(t *testing.T) {
	compared := 0
	for _, answer := range capturecompare.Answers(t, captures) {
		if len(answer.Queued) == 0 {
			continue
		}
		if len(answer.Queued) != 1 {
			t.Fatalf("%s: Python queued %d messages", answer.Label, len(answer.Queued))
		}
		documentID := idInPath(t, answer.Request.Path)
		key, envelope := capturecompare.Published(t, "surfsense",
			DeletionOf(documentID, celeryqueue.LogContextFor(
				answer.Request.Path, "req_000000000000", callers[answer.Request.As])))
		if key != answer.Queued[0].Queue {
			t.Errorf("%s: published to %q, Python to %q", answer.Label, key, answer.Queued[0].Queue)
		}
		// The captured request id is blanked in both, so the one passed above
		// is only a value of the right shape.
		if envelope != answer.Queued[0].Envelope {
			t.Errorf("%s: this tier published\n%s\nPython published\n%s",
				answer.Label, envelope, answer.Queued[0].Envelope)
		}
		compared++
	}
	if compared == 0 {
		t.Fatal("no captured message to compare against")
	}
}

// The accounts the captures were taken as. The user id travels in the log
// context header, so the message depends on who asked.
var callers = map[string]string{
	"owner":   "11111111-1111-4111-8111-111111111111",
	"member":  "22222222-2222-4222-8222-222222222222",
	"remover": "44444444-4444-4444-8444-444444444444",
}

// Every answer the Python route gave is one this slice can still produce: the
// body for the ones that succeeded, and the status and wording for the rest.
func TestEveryCapturedAnswerIsStillThisSlicesAnswer(t *testing.T) {
	for _, answer := range capturecompare.Answers(t, captures) {
		switch answer.Status {
		case http.StatusOK:
			body, err := pythonjson.Marshal(Message())
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(body) != answer.Body {
				t.Errorf("%s: this slice renders %s, Python rendered %s", answer.Label, body, answer.Body)
			}
		case http.StatusNotFound:
			expectRefusal(t, answer.Label, answer.Body, "NOT_FOUND", NotFound(idInPath(t, answer.Request.Path)))
		case http.StatusConflict:
			// Which of the two conflicts it is comes from the state the
			// document was seeded in, so the wording is matched rather than
			// chosen.
			if !strings.Contains(answer.Body, BeingProcessed) && !strings.Contains(answer.Body, AlreadyDeleting) {
				t.Errorf("%s: Python refused with wording this slice does not have: %s", answer.Label, answer.Body)
			}
		case http.StatusForbidden:
			// A caller with no membership is refused by the shared gate; a
			// member whose role may not delete gets this route's own wording.
			if answer.Request.As == "member" {
				expectRefusal(t, answer.Label, answer.Body, "FORBIDDEN", DeleteDenied)
			}
		}
	}
}

func expectRefusal(t *testing.T, label, body, code, message string) {
	t.Helper()
	for _, want := range []string{
		`"code":"` + code + `"`,
		`"message":"` + strings.ReplaceAll(message, `"`, `\"`) + `"`,
		`"detail":"` + strings.ReplaceAll(message, `"`, `\"`) + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("%s: the captured body has no %s:\n%s", label, want, body)
		}
	}
}

func idInPath(t *testing.T, path string) int64 {
	t.Helper()
	_, last, found := strings.Cut(path, "/documents/")
	if !found {
		t.Fatalf("%q names no document", path)
	}
	id, err := strconv.ParseInt(last, 10, 64)
	if err != nil {
		t.Fatalf("%q: %v", path, err)
	}
	return id
}
