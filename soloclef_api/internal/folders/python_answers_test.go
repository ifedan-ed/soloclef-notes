package folders

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

// The captures are what the Python route answered to nine deletes, taken by
// asking it each of them against the same seeded database that the whole
// comparison used (custom/docs/go-api-tier.md, "How phase 7 was proved"). The
// comparison was made there; this keeps it.
//
// The enqueued message is the part to fear. The delete's answer says only how
// many documents it queued, so a response that matches while the message
// differs is a folder that looks deleted and is never deleted — and the
// message carries two lists whose *order* is a query's rather than a sort's.
func TestTheEnqueuedMessageIsTheOnePythonPublished(t *testing.T) {
	compared := 0
	for _, answer := range capturecompare.Answers(t, captures) {
		if len(answer.Queued) == 0 {
			continue
		}
		if len(answer.Queued) != 1 {
			t.Fatalf("%s: Python queued %d messages", answer.Label, len(answer.Queued))
		}
		documentIDs, subtreeIDs := queuedLists(t, answer.Queued[0].Envelope)
		key, envelope := capturecompare.Published(t, "surfsense",
			DeletionOf(documentIDs, subtreeIDs, celeryqueue.LogContextFor(
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

// Every answer the Python route gave is one this slice can still produce: the
// body for the ones that succeeded, and the status and wording for the rest.
func TestEveryCapturedAnswerIsStillThisSlicesAnswer(t *testing.T) {
	for _, answer := range capturecompare.Answers(t, captures) {
		switch answer.Status {
		case http.StatusOK:
			queued := queuedCount(t, answer.Body)
			body, err := pythonjson.Marshal(DeletionAnswer(queued))
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if string(body) != answer.Body {
				t.Errorf("%s: this slice renders %s, Python rendered %s", answer.Label, body, answer.Body)
			}
		case http.StatusNotFound:
			expectRefusal(t, answer.Label, answer.Body, "NOT_FOUND", NotFound)
		case http.StatusForbidden:
			// A caller with no membership is refused by the shared gate; a
			// member whose role may not delete gets this route's own wording.
			if answer.Request.As == "reader" {
				expectRefusal(t, answer.Label, answer.Body, "FORBIDDEN", DeleteDenied)
			}
		}
	}
}

// The accounts the captures were taken as. The user id travels in the log
// context header, so the message depends on who asked.
var callers = map[string]string{
	"owner":  "11111111-1111-4111-8111-111111111111",
	"editor": "22222222-2222-4222-8222-222222222222",
	"reader": "33333333-3333-4333-8333-333333333333",
}

// queuedLists reads the two lists back out of the captured envelope's own
// argsrepr and kwargsrepr, so the test republishes what Python published
// rather than a list written here — which is the only way the *order* of
// either list is part of the comparison.
func queuedLists(t *testing.T, envelope string) (documentIDs, subtreeIDs []int64) {
	t.Helper()
	return idsIn(t, envelope, `"argsrepr": "([`, `],)"`),
		idsIn(t, envelope, `"kwargsrepr": "{'folder_subtree_ids': [`, `]}"`)
}

func idsIn(t *testing.T, envelope, opening, closing string) []int64 {
	t.Helper()
	_, after, found := strings.Cut(envelope, opening)
	if !found {
		t.Fatalf("the captured envelope has no %s:\n%s", opening, envelope)
	}
	inside, _, found := strings.Cut(after, closing)
	if !found {
		t.Fatalf("the captured envelope has no %s:\n%s", closing, envelope)
	}
	if strings.TrimSpace(inside) == "" {
		return nil
	}
	var ids []int64
	for _, text := range strings.Split(inside, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			t.Fatalf("%q is not an id: %v", text, err)
		}
		ids = append(ids, id)
	}
	return ids
}

// queuedCount reads the count back out of the captured body, so the renderer
// is compared and not the count.
func queuedCount(t *testing.T, body string) int {
	t.Helper()
	_, after, found := strings.Cut(body, `"documents_queued_for_deletion":`)
	if !found {
		t.Fatalf("the captured body names no count:\n%s", body)
	}
	digits, _, _ := strings.Cut(after, "}")
	count, err := strconv.Atoi(strings.TrimSpace(digits))
	if err != nil {
		t.Fatalf("%q is not a count: %v", digits, err)
	}
	return count
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
