package celery

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func decodeResult(t *testing.T, r Result) map[string]any {
	t.Helper()
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	return out
}

func TestResultKeyMatchesThePythonBackend(t *testing.T) {
	if got := ResultKey("abc"); got != "celery-task-meta-abc" {
		t.Errorf("ResultKey = %q", got)
	}
}

func TestSuccessRowCarriesEveryExtendedField(t *testing.T) {
	msg, err := Decode([]byte(liveEnvelope))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	res := ResultFor(msg, "soloclef-worker@host", StateSuccess)
	res.Value = map[string]any{"ok": true}
	res.DoneAt = time.Date(2026, 11, 7, 20, 30, 0, 0, time.UTC)

	out := decodeResult(t, res)
	for field, want := range map[string]any{
		"status":  "SUCCESS",
		"task_id": "23fbf912-949d-4146-ad96-5911471d5da1",
		"name":    "probe.wire_format",
		"worker":  "soloclef-worker@host",
		"queue":   "soloclef.goprobe",
	} {
		if out[field] != want {
			t.Errorf("%s = %#v, want %#v", field, out[field], want)
		}
	}
	if out["traceback"] != nil {
		t.Errorf("traceback should be null on success, got %#v", out["traceback"])
	}
	if out["date_done"] == nil {
		t.Error("a finished task needs date_done")
	}
	if _, ok := out["children"].([]any); !ok {
		t.Errorf("children must be a list, got %#v", out["children"])
	}
}

func TestStartedRowHasNoCompletionTime(t *testing.T) {
	res := Result{TaskID: "1", State: StateStarted, DoneAt: time.Now()}
	if out := decodeResult(t, res); out["date_done"] != nil {
		t.Errorf("date_done = %#v on a task still running", out["date_done"])
	}
}

func TestEmptyArgsAndKwargsSerialiseAsContainersNotNull(t *testing.T) {
	out := decodeResult(t, Result{TaskID: "1", State: StateSuccess})
	if _, ok := out["args"].([]any); !ok {
		t.Errorf("args = %#v, want []", out["args"])
	}
	if _, ok := out["kwargs"].(map[string]any); !ok {
		t.Errorf("kwargs = %#v, want {}", out["kwargs"])
	}
}

func TestFailureRowReadsBackAsAnException(t *testing.T) {
	res := Result{
		TaskID:    "1",
		State:     StateFailure,
		Value:     ErrorValue("SoftTimeLimitExceeded", errors.New("render exceeded 7h50m")),
		Traceback: "goroutine 1 [running]:",
		DoneAt:    time.Now(),
	}
	out := decodeResult(t, res)
	value, ok := out["result"].(map[string]any)
	if !ok {
		t.Fatalf("result = %#v", out["result"])
	}
	if value["exc_type"] != "SoftTimeLimitExceeded" {
		t.Errorf("exc_type = %#v", value["exc_type"])
	}
	if msgs, ok := value["exc_message"].([]any); !ok || len(msgs) != 1 || msgs[0] != "render exceeded 7h50m" {
		t.Errorf("exc_message = %#v", value["exc_message"])
	}
	if out["traceback"] == nil {
		t.Error("a failure should keep its traceback")
	}
}

func TestErrorValueNamesSomethingEvenWithNothingToGoOn(t *testing.T) {
	value := ErrorValue("", nil)
	if value["exc_type"] != "WorkerError" {
		t.Errorf("exc_type = %#v", value["exc_type"])
	}
	if msgs := value["exc_message"].([]any); len(msgs) != 1 || msgs[0] != "" {
		t.Errorf("exc_message = %#v", value["exc_message"])
	}
}

func TestOnlyEndStatesAreTerminal(t *testing.T) {
	for _, s := range []State{StateSuccess, StateFailure, StateRevoked} {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
	}
	for _, s := range []State{StateStarted, StateRetry} {
		if s.Terminal() {
			t.Errorf("%s should not be terminal", s)
		}
	}
}
