package process

import (
	"strings"
	"testing"
)

// The name goes in every result row as Celery's hostname does, so it says
// which worker ran the job without having to be configured per container.
func TestTheWorkerNamesItselfAfterItsHostUnlessItIsTold(t *testing.T) {
	told := WorkerName(func(string) string { return "go-worker@somewhere" })
	if told != "go-worker@somewhere" {
		t.Errorf("name = %q, want the one it was given", told)
	}
	derived := WorkerName(func(string) string { return "" })
	if !strings.HasPrefix(derived, "go-worker@") || len(derived) <= len("go-worker@") {
		t.Errorf("name = %q, want go-worker@<host>", derived)
	}
}
