package deliverables

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model/modeltest"
	"git.danvics.com/danvics/soloclef/soloclef_studio/sandbox"
)

// fullService is the sandbox service of an account at its limit: a call
// that does not wait is refused at once, and one that waits is answered
// with a sandbox when placeFree says a place freed, or refused when not.
type fullService struct {
	mu        sync.Mutex
	placeFree bool
	// waits is the waitSeconds of each find-or-create (0: not waiting).
	waits    []int64
	released []string
}

func (f *fullService) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method == http.MethodDelete {
		f.released = append(f.released, strings.TrimPrefix(r.URL.Path, "/v1/sandboxes/"))
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var body struct {
		WaitSeconds int64 `json:"waitSeconds"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.waits = append(f.waits, body.WaitSeconds)
	if body.WaitSeconds > 0 && f.placeFree {
		fmt.Fprint(w, `{"id":"box-1","created":true}`)
		return
	}
	w.WriteHeader(http.StatusTooManyRequests)
	fmt.Fprint(w, `{"error":"account_full","message":"full"}`)
}

func fullWorkshop(t *testing.T, service *fullService) Sandboxes {
	t.Helper()
	server := httptest.NewServer(service)
	t.Cleanup(server.Close)
	client, err := sandbox.New(sandbox.Settings{
		URL: server.URL, APIKey: "key", CPU: "2", Memory: "2Gi",
		IdleTTL: 10 * time.Minute, CommandTimeout: 10 * time.Second, MaxPerAccount: 1,
	}, &http.Client{})
	if err != nil {
		t.Fatal(err)
	}
	return Sandboxes{Client: client}
}

// recordedWaits is what a run's Waiting was told, in order.
type recordedWaits struct {
	mu   sync.Mutex
	told []bool
}

func (r *recordedWaits) wait(limit time.Duration) SandboxWait {
	return SandboxWait{For: limit, Waiting: func(_ context.Context, waiting bool) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.told = append(r.told, waiting)
	}}
}

// A run nobody watches asks once without waiting, then waits, and says so
// at both ends; it gets the sandbox the moment the service answers.
func TestAnUnattendedRunWaitsForAPlaceAndSaysSo(t *testing.T) {
	service := &fullService{placeFree: true}
	workshop := fullWorkshop(t, service)
	waits := &recordedWaits{}
	bench, err := workshop.Bench(context.Background(), Owner{UserID: "u", ThreadID: 7}, waits.wait(UnattendedSandboxWait))
	if err != nil || bench.ID() != "box-1" {
		t.Fatalf("bench %v, err %v", bench, err)
	}
	if want := []int64{0, int64(UnattendedSandboxWait / time.Second)}; fmt.Sprint(service.waits) != fmt.Sprint(want) {
		t.Errorf("asked with waits %v, want %v", service.waits, want)
	}
	if fmt.Sprint(waits.told) != "[true false]" {
		t.Errorf("Waiting was told %v, want [true false]", waits.told)
	}
}

// A wait that runs out is the busy refusal, with its code; the run's
// failures note it, and the model is told the server's sentence.
func TestAWaitThatRunsOutIsNotedAsSandboxBusy(t *testing.T) {
	service := &fullService{}
	s, _ := specialist(t, modeltest.NewScript(), &ledger{})
	s.Workshop = fullWorkshop(t, service)
	waits := &recordedWaits{}
	s.SandboxWait = waits.wait(time.Second)
	s.Failures = &Failures{}
	_, err := s.bench(context.Background(), Owner{UserID: "u", ThreadID: 7})
	busy, _ := FailureSentence(FailureSandboxBusy)
	if err == nil || err.Error() != busy+refusedDocumentNote {
		t.Fatalf("err = %v", err)
	}
	if failure, noted := s.Failures.Last(); !noted || failure.Code != FailureSandboxBusy {
		t.Errorf("noted %+v (%v), want %s", failure, noted, FailureSandboxBusy)
	}
	if fmt.Sprint(waits.told) != "[true false]" {
		t.Errorf("Waiting was told %v", waits.told)
	}
}

// A chat turn does not wait: one call, refused at once, with the sentence
// for a person watching, and nothing noted (a chat has no Failures).
func TestAChatTurnIsToldAtOnce(t *testing.T) {
	service := &fullService{placeFree: true}
	s, _ := specialist(t, modeltest.NewScript(), &ledger{})
	s.Workshop = fullWorkshop(t, service)
	_, err := s.bench(context.Background(), Owner{UserID: "u", ThreadID: 7})
	if err == nil || err.Error() != sandboxFullSentence+refusedDocumentNote {
		t.Fatalf("err = %v", err)
	}
	if len(service.waits) != 1 || service.waits[0] != 0 {
		t.Errorf("asked %v, want one call that does not wait", service.waits)
	}
}

// Releasing a run's sandbox removes its thread's.
func TestReleaseRemovesTheThreadsSandbox(t *testing.T) {
	service := &fullService{}
	s, _ := specialist(t, modeltest.NewScript(), &ledger{})
	s.Workshop = fullWorkshop(t, service)
	if err := s.ReleaseSandbox(context.Background(), Owner{UserID: "u", ThreadID: 42}); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(service.released) != "[thread/42]" {
		t.Errorf("released %v", service.released)
	}
	if err := (NoSandbox{}).Release(context.Background(), Owner{}); err != nil {
		t.Errorf("NoSandbox: %v", err)
	}
}

func TestFailuresKeepTheLastAndNilNotesNothing(t *testing.T) {
	var none *Failures
	none.note(FailureStorage, errors.New("x"))
	if _, noted := none.Last(); noted {
		t.Error("a nil collector noted something")
	}
	f := &Failures{}
	if _, noted := f.Last(); noted {
		t.Error("an empty collector has a failure")
	}
	f.note(FailureSandbox, errors.New("first"))
	f.note(FailureStorage, errors.New("second"))
	if got, _ := f.Last(); got.Code != FailureStorage || got.Cause != "second" {
		t.Errorf("last = %+v", got)
	}
	for _, code := range []string{FailureSandboxBusy, FailureNoSandbox, FailureSandbox, FailurePreview, FailureDrawing, FailureStorage} {
		if sentence, known := FailureSentence(code); !known || sentence == "" {
			t.Errorf("%s has no sentence", code)
		}
	}
}
