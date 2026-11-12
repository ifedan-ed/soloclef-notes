package sandbox

import (
	"context"
	"errors"
	"io"
	"strconv"
	"testing"
	"time"
)

// A job attempt has a sandbox of its own, found again by its name, labelled
// so that it is neither a thread's nor counted against the account's limit
// on conversations' sandboxes.
func TestAJobAttemptHasASandboxOfItsOwn(t *testing.T) {
	s, client := stubClient(t)
	ctx := context.Background()

	first, err := client.ForJob(ctx, "AAAAAAAA-0000-4000-8000-000000000001", "deliverable-job-7-attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	again, err := client.ForJob(ctx, ada, "deliverable-job-7-attempt-1")
	if err != nil || again.ID != first.ID {
		t.Fatalf("found %v (%v), want %s", again, err, first.ID)
	}
	s.mu.Lock()
	labels := s.boxes[first.ID].metadata
	s.mu.Unlock()
	if labels[jobLabel] != "deliverable-job-7-attempt-1" || labels[jobAccountLabel] != ada {
		t.Errorf("labels = %v", labels)
	}
	if _, isThread := labels[threadLabel]; isThread {
		t.Errorf("a job's sandbox carries a thread label: %v", labels)
	}
	if _, counted := labels[accountLabel]; counted {
		t.Errorf("a job's sandbox counts against the account's conversations: %v", labels)
	}

	// The account's two conversation sandboxes are still free to make.
	for thread := int64(1); thread <= 2; thread++ {
		if _, err := client.ForThread(ctx, ada, thread); err != nil {
			t.Fatalf("thread %d: %v", thread, err)
		}
	}

	// Another attempt is another sandbox.
	second, err := client.ForJob(ctx, ada, "deliverable-job-7-attempt-2")
	if err != nil || second.ID == first.ID {
		t.Fatalf("the second attempt got %v (%v)", second, err)
	}

	if err := client.TerminateJob(ctx, "deliverable-job-7-attempt-1"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	_, firstLeft := s.boxes[first.ID]
	_, secondLeft := s.boxes[second.ID]
	left := len(s.boxes)
	s.mu.Unlock()
	if firstLeft || !secondLeft || left != 3 {
		t.Errorf("after removing attempt 1: first left %v, second left %v, %d boxes", firstLeft, secondLeft, left)
	}
	// Removing an attempt that has none is not an error.
	if err := client.TerminateJob(ctx, "deliverable-job-7-attempt-1"); err != nil {
		t.Errorf("a second removal: %v", err)
	}
}

// A job's name becomes a label value, so anything else is refused before a
// request is made.
func TestAJobNameMustBeALabel(t *testing.T) {
	_, client := stubClient(t)
	for _, name := range []string{"", "Job-1", "job 1", "job_1", "-job", "job-", "a/b"} {
		if _, err := client.ForJob(context.Background(), ada, name); err == nil {
			t.Errorf("%q was accepted", name)
		}
		if err := client.TerminateJob(context.Background(), name); err == nil {
			t.Errorf("%q was accepted for removal", name)
		}
	}
}

// A file streams out whole, a missing one is ErrNotFound, and a command can
// be given a limit of its own.
func TestAJobStreamsItsFileAndRunsLongCommands(t *testing.T) {
	_, client := stubClient(t)
	ctx := context.Background()
	session, err := client.ForJob(ctx, ada, "deliverable-job-9-attempt-1")
	if err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 3<<20)
	for i := range data {
		data[i] = byte(i)
	}
	if err := session.WriteFile(ctx, "/workspace/out.mp4", data); err != nil {
		t.Fatal(err)
	}
	stream, err := session.OpenFile(ctx, "/workspace/out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(stream)
	stream.Close()
	if err != nil || len(got) != len(data) || got[len(got)-1] != data[len(data)-1] {
		t.Errorf("streamed %d bytes (%v), want %d", len(got), err, len(data))
	}
	if _, err := session.OpenFile(ctx, "/workspace/missing.mp4"); !errors.Is(err, ErrNotFound) {
		t.Errorf("a missing file: %v", err)
	}
	if result, err := session.RunWithin(ctx, "node render.mjs", time.Hour); err != nil || !result.OK() {
		t.Errorf("a long command: %+v (%v)", result, err)
	}
	if _, err := session.RunWithin(ctx, "true", 0); err == nil {
		t.Error("a command with no limit was sent")
	}
}

// TestARealJobSandbox is the job's contract against the real server and
// image: created under its name, found again, a long command given its own
// limit, a file streamed out, and removed.
func TestARealJobSandbox(t *testing.T) {
	client := realClient(t, "test-job-"+strconv.FormatInt(time.Now().UnixNano(), 10))
	ctx := context.Background()
	name := "deliverable-job-" + strconv.FormatInt(time.Now().UnixNano()%1_000_000_000, 10) + "-attempt-1"
	t.Cleanup(func() { _ = client.TerminateJob(context.Background(), name) })
	started := time.Now()
	session, err := client.ForJob(ctx, ada, name)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("create: %s", time.Since(started).Round(time.Millisecond))
	again, err := client.ForJob(ctx, ada, name)
	if err != nil || again.ID != session.ID {
		t.Fatalf("found %v (%v), want %s", again, err, session.ID)
	}
	if result, err := session.RunWithin(ctx, "head -c 5000000 /dev/urandom > /workspace/out.bin && sha256sum /workspace/out.bin", 3*time.Minute); err != nil || !result.OK() {
		t.Fatalf("a long command: %+v (%v)", result, err)
	}
	started = time.Now()
	stream, err := session.OpenFile(ctx, "/workspace/out.bin")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(stream)
	stream.Close()
	if err != nil || len(got) != 5_000_000 {
		t.Fatalf("streamed %d bytes (%v)", len(got), err)
	}
	t.Logf("5 MB streamed out: %s", time.Since(started).Round(time.Millisecond))
	if err := client.TerminateJob(ctx, name); err != nil {
		t.Fatal(err)
	}
	if _, err := session.Run(ctx, "true"); err == nil {
		t.Error("a command ran in a removed sandbox")
	}
}
