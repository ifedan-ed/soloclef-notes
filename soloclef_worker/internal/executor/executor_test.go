package executor

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"git.danvics.com/danvics/surfsense/soloclef_worker/internal/celery"
)

// The whole seam in one test: the task name reaches the process as its
// argument, the delivery reaches it on stdin, and what it reports becomes the
// row a client reads.
func TestASuccessfulTaskProcessBecomesASuccessRow(t *testing.T) {
	captured := filepath.Join(t.TempDir(), "stdin.json")
	argv := filepath.Join(t.TempDir(), "argv")
	command := fakeChild(t, `
cat > `+captured+`
printf '%s' "$1" > `+argv+`
echo '{"status": "SUCCESS", "result": {"indexed": 3}}'
`)
	executor, rows := testExecutor(t, testConfig(t, command))
	msg := probeMessage(t)

	if err := executor.Run(context.Background(), msg); err != nil {
		t.Fatalf("Run: %v", err)
	}

	name, err := os.ReadFile(argv)
	if err != nil {
		t.Fatalf("reading the task name the process was given: %v", err)
	}
	if string(name) != msg.Task {
		t.Errorf("the process was asked to run %q, want %q", name, msg.Task)
	}

	raw, err := os.ReadFile(captured)
	if err != nil {
		t.Fatalf("reading what was fed to the process: %v", err)
	}
	var call Call
	if err := json.Unmarshal(raw, &call); err != nil {
		t.Fatalf("stdin was not the call contract: %v\n%s", err, raw)
	}
	if call.ID != msg.ID || call.Retries != msg.Retries || call.Queue != msg.Queue {
		t.Errorf("call = %#v", call)
	}
	if len(call.Args) != 2 || call.Args[1] != "two" || call.Kwargs["k"] != true {
		t.Errorf("args/kwargs = %#v / %#v", call.Args, call.Kwargs)
	}
	if call.HardTimeLimit <= 0 {
		t.Error("the process must be told how long it has")
	}

	written := rows.all()
	if len(written) != 2 || written[0].State != celery.StateStarted || written[1].State != celery.StateSuccess {
		t.Fatalf("rows = %#v, want STARTED then SUCCESS", written)
	}
	if written[0].Name != msg.Task || written[1].Worker != "go-worker-test" {
		t.Errorf("rows carry the wrong task or worker: %#v", written)
	}
	value, ok := written[1].Value.(map[string]any)
	if !ok || value["indexed"] != 3.0 {
		t.Errorf("the task's return value did not survive: %#v", written[1].Value)
	}
	if written[1].DoneAt.IsZero() {
		t.Error("a finished row needs date_done")
	}
}
