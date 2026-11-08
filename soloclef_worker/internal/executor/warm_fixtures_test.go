package executor

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
)

// fakeWarmChild writes a shell script that stands in for
// `python -m app.tasks.serve`: it says it is ready once, then runs the per-job
// body for every request line it is given. A shell is used for the same reason
// phase 3 used one — signals, process groups and grandchildren read plainly in
// it — and `report` is the one line the worker is waiting for.
//
// preamble runs before the ready line, which is where a real child's 27-33s
// of imports happen.
func fakeWarmChild(t *testing.T, preamble, perJob string) []string {
	t.Helper()
	return fakeChild(t, `
report() { echo '{"id":"'"$id"'","report":'"$1"'}'; }
succeed() { report '{"status":"SUCCESS","result":"ok"}'; }
`+preamble+`
echo '{"ready":true,"pid":'$$'}'
ran=0
while IFS= read -r line; do
  id=$(printf '%s\n' "$line" | sed -n 's/.*"id":"\([^"]*\)".*/\1/p')
  ran=$((ran+1))
`+perJob+`
done
`)
}

func testPoolConfig(t *testing.T, command []string) Config {
	t.Helper()
	cfg := testConfig(t, command)
	cfg.PoolSize = 1
	cfg.ChildReadyTimeout = 20 * time.Second
	return cfg
}

// freshMessage is a delivery of its own, because a warm child's reports are
// matched to the job that is waiting for them by id.
func freshMessage(t *testing.T, id string) *celery.Message {
	t.Helper()
	msg := probeMessage(t)
	msg.ID = id
	msg.RootID = id
	msg.DeliveryTag = id
	return msg
}

// pidsFrom reads the pids a fake child appended, one per job, which is how a
// test tells reuse from replacement.
func pidsFrom(t *testing.T, path string) []int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("no pids were written to %s: %v", path, err)
	}
	var pids []int
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			t.Fatalf("%s holds %q, not a pid", path, line)
		}
		pids = append(pids, pid)
	}
	return pids
}

func distinct(pids []int) int {
	seen := map[int]bool{}
	for _, pid := range pids {
		seen[pid] = true
	}
	return len(seen)
}
