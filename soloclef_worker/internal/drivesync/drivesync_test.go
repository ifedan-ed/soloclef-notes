package drivesync

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/drivesyncjob"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
)

// The whole rule for a file Drive has and the account has a document for.
func TestWhatASyncDoesAboutOneFile(t *testing.T) {
	ready := Document{State: "ready", Name: "Report.pdf", MD5: "aaa", Modified: "2026-11-01T10:00:00.000Z"}
	native := Document{State: "ready", Name: "Plan", Modified: "2026-11-01T10:00:00.000Z"}
	for name, tc := range map[string]struct {
		document Document
		file     remoteFile
		want     disposition
	}{
		"the same bytes":                {ready, remoteFile{Name: "Report.pdf", MD5: "aaa", Modified: "2026-11-09T10:00:00.000Z"}, leave},
		"other bytes":                   {ready, remoteFile{Name: "Report.pdf", MD5: "bbb", Modified: "2026-11-01T10:00:00.000Z"}, fetch},
		"the same bytes, renamed":       {ready, remoteFile{Name: "Final.pdf", MD5: "aaa"}, rename},
		"a Google Doc, untouched":       {native, remoteFile{Name: "Plan", Modified: "2026-11-01T10:00:00.000Z"}, leave},
		"a Google Doc, edited":          {native, remoteFile{Name: "Plan", Modified: "2026-11-02T10:00:00.000Z"}, fetch},
		"a Google Doc, renamed":         {native, remoteFile{Name: "The plan", Modified: "2026-11-01T10:00:00.000Z"}, rename},
		"a checksum where none was":     {native, remoteFile{Name: "Plan", MD5: "ccc", Modified: "2026-11-01T10:00:00.000Z"}, fetch},
		"waiting for its file":          {Document{State: "pending", Stage: "downloading", MD5: "aaa"}, remoteFile{MD5: "aaa"}, fetch},
		"never stored, nothing to tell": {Document{State: "failed"}, remoteFile{Name: "x"}, fetch},
		"failed, and changed since":     {Document{State: "failed", MD5: "aaa"}, remoteFile{MD5: "bbb"}, fetch},
		"being deleted":                 {Document{State: "deleting", MD5: "aaa"}, remoteFile{MD5: "bbb"}, leave},
		"being indexed, unchanged":      {Document{State: "processing", Name: "a", MD5: "aaa"}, remoteFile{Name: "a", MD5: "aaa"}, leave},
	} {
		if got := decide(tc.document, tc.file); got != tc.want {
			t.Errorf("%s: decided %d, want %d", name, got, tc.want)
		}
	}
}

// Each way Drive says no is told apart, and none of them carries the
// reply's body or the request's address into the error.
func TestDrivesRefusalsAreToldApart(t *testing.T) {
	var status int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer the-token" {
			t.Errorf("the request carried %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"error": {"message": "a secret detail about file-secret-0001"}}`)
	}))
	defer server.Close()
	api := drive{base: server.URL, token: "the-token", http: NewHTTP(time.Second)}
	for code, want := range map[int]error{401: errAuth, 404: errGone, 403: errRefused, 429: errUnavailable, 500: errUnavailable, 503: errUnavailable} {
		status = code
		_, err := api.file(t.Context(), "file-secret-0001")
		if !errors.Is(err, want) {
			t.Errorf("a %d gave %v", code, err)
		}
		if strings.Contains(err.Error(), "secret") {
			t.Errorf("the error for a %d repeats Drive's reply or the file: %v", code, err)
		}
	}
	// The feed's own refusal: a page token Drive no longer knows.
	for _, code := range []int{400, 410} {
		status = code
		if _, _, _, err := api.changes(t.Context(), "p1"); !errors.Is(err, errFeedPosition) {
			t.Errorf("a %d from the feed gave %v", code, err)
		}
	}
	// The same status about a file is not a feed position.
	status = 400
	if _, err := api.file(t.Context(), "file-secret-0001"); errors.Is(err, errFeedPosition) || err == nil {
		t.Errorf("a 400 about a file gave %v", err)
	}
	server.Close()
	if _, err := api.file(t.Context(), "file-secret-0001"); !errors.Is(err, errUnavailable) || strings.Contains(err.Error(), "127.0.0.1") {
		t.Errorf("a Drive that is down gave %v", err)
	}
}

// A reply that redirects is not followed: the account's token must not be
// sent wherever a reply points.
func TestAnAccessTokenIsNeverSentToARedirectsTarget(t *testing.T) {
	elsewhere := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		t.Errorf("the redirect was followed, carrying %q", r.Header.Get("Authorization"))
	}))
	defer elsewhere.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/steal", http.StatusFound)
	}))
	defer server.Close()
	api := drive{base: server.URL, token: "the-token", http: NewHTTP(time.Second)}
	if _, err := api.open(t.Context(), "file-0000001", ""); err == nil {
		t.Error("a redirect was read as a file")
	}
}

// The cap is on the bytes themselves: exactly the limit passes, one more
// does not.
func TestADownloadIsCutOffPastTheLimit(t *testing.T) {
	exactly, err := io.ReadAll(&capped{body: strings.NewReader("12345"), limit: 5})
	if err != nil || string(exactly) != "12345" {
		t.Errorf("a file of exactly the limit read as %q, %v", exactly, err)
	}
	if _, err := io.ReadAll(&capped{body: strings.NewReader("123456"), limit: 5}); !errors.Is(err, errTooLarge) {
		t.Errorf("a file one byte over the limit gave %v", err)
	}
}

func TestTheSettings(t *testing.T) {
	directory := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(directory, name)
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	complete := map[string]string{
		Enabled: "true", ClientID: "1-web.apps.googleusercontent.com", ClientSecretFile: write("client", "client-secret\n"),
		SecretKeyFile: write("key", "the-key\n"), MaxFileBytes: "52428800", Concurrency: "4",
	}
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	config, err := LoadConfig(env(complete))
	if err != nil || !config.Enabled || config.ClientSecret != "client-secret" || config.SecretKey != "the-key" ||
		config.MaxFileBytes != 52428800 || config.Concurrency != 4 {
		t.Fatalf("the settings read as %+v (%v)", config, err)
	}
	// Off is said, and then nothing else is asked for.
	if config, err := LoadConfig(env(map[string]string{Enabled: "false"})); err != nil || config.Enabled {
		t.Errorf("switched off read as %+v (%v)", config, err)
	}
	// Not said at all is an error: off is never inferred.
	if _, err := LoadConfig(env(map[string]string{})); err == nil || !strings.Contains(err.Error(), Enabled) {
		t.Errorf("an unset switch gave %v", err)
	}
	// Everything wrong is said at once, by name, and no secret is quoted.
	broken := map[string]string{Enabled: "true", ClientSecretFile: filepath.Join(directory, "absent"),
		SecretKeyFile: write("empty", "\n"), MaxFileBytes: "lots", Concurrency: "0"}
	_, err = LoadConfig(env(broken))
	for _, name := range []string{ClientID, ClientSecretFile, SecretKeyFile, MaxFileBytes, Concurrency} {
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("the error does not name %s: %v", name, err)
		}
	}
}

// fakeSchedule hands out the connections it was given, once.
type fakeSchedule struct {
	due []Due
	err error
}

func (f *fakeSchedule) TakeDue(_ context.Context, limit int) ([]Due, error) {
	if f.err != nil {
		return nil, f.err
	}
	taken := f.due[:min(limit, len(f.due))]
	f.due = f.due[len(taken):]
	return taken, nil
}

// fakeSender records what was published, and refuses what it is told to.
type fakeSender struct {
	sent   []produce.Message
	refuse func(produce.Message) bool
}

func (f *fakeSender) Send(_ context.Context, message produce.Message) (string, error) {
	if f.refuse != nil && f.refuse(message) {
		return "", errors.New("the broker is down")
	}
	f.sent = append(f.sent, message)
	return "id", nil
}

// fakeFence is a fence in memory.
type fakeFence struct {
	taken    map[int64]bool
	released []int64
}

func (f *fakeFence) Take(_ context.Context, moment time.Time, _ time.Duration) (bool, error) {
	if f.taken[moment.Unix()] {
		return false, nil
	}
	f.taken[moment.Unix()] = true
	return true, nil
}

func (f *fakeFence) Release(_ context.Context, moment time.Time) error {
	delete(f.taken, moment.Unix())
	f.released = append(f.released, moment.Unix())
	return nil
}

const (
	alice = "11111111-1111-4111-8111-111111111111"
	bob   = "22222222-2222-4222-8222-222222222222"
)

func newTick(schedule *fakeSchedule, sender *fakeSender, fence *fakeFence, now time.Time) *Tick {
	return &Tick{Schedule: schedule, Publisher: sender, Fence: fence, Every: 5 * time.Minute, Batch: 10,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Now: func() time.Time { return now }}
}

// A tick schedules its successor for the next whole interval, once
// however many workers ask, and publishes a sync for each due connection
// under the account that owns it.
func TestATickSchedulesTheNextAndPublishesWhatIsDue(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 3, 20, 0, time.UTC)
	schedule := &fakeSchedule{due: []Due{{ConnectorID: 7, UserID: alice}, {ConnectorID: 9, UserID: bob}}}
	sender, fence := &fakeSender{}, &fakeFence{taken: map[int64]bool{}}
	tick := newTick(schedule, sender, fence, now)
	if err := tick.Check(); err != nil {
		t.Fatal(err)
	}
	if err := tick.Run(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 3 {
		t.Fatalf("published %d messages, want the next tick and two syncs", len(sender.sent))
	}
	next := sender.sent[0]
	if next.Task != TickTask || next.Countdown != 100*time.Second || next.ID != TickTask+":1794474300" || !next.IgnoreResult {
		t.Errorf("the next tick is %+v", next)
	}
	for index, want := range []drivesyncjob.Request{{ConnectorID: 7, UserID: alice}, {ConnectorID: 9, UserID: bob}} {
		message := sender.sent[index+1]
		read, err := drivesyncjob.Read(viaJSON(t, message.Kwargs))
		if message.Task != Task || err != nil || read.ConnectorID != want.ConnectorID || read.UserID != want.UserID || len(read.FileIDs) != 0 {
			t.Errorf("sync %d is %+v (%v)", index, message, err)
		}
	}
	// A second worker starting in the same interval adds no second tick.
	if err := tick.Seed(t.Context()); err != nil || len(sender.sent) != 3 {
		t.Errorf("a second seed published again (%d messages, %v)", len(sender.sent), err)
	}
}

// A tick that cannot publish its successor goes back on the queue and
// gives the moment back, so the chain cannot end on a broker that blinked.
func TestATickThatCannotScheduleItsSuccessorIsRunAgain(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 3, 20, 0, time.UTC)
	sender := &fakeSender{refuse: func(message produce.Message) bool { return message.Task == TickTask }}
	fence := &fakeFence{taken: map[int64]bool{}}
	tick := newTick(&fakeSchedule{due: []Due{{ConnectorID: 7, UserID: alice}}}, sender, fence, now)
	err := tick.Run(t.Context(), nil)
	if !errors.Is(err, broker.ErrRequeue) {
		t.Fatalf("the tick ended with %v", err)
	}
	if len(fence.released) != 1 || len(fence.taken) != 0 {
		t.Errorf("the fence was kept for a tick that was never published: %+v", fence)
	}
	// Its own work was still done.
	if len(sender.sent) != 1 || sender.sent[0].Task != Task {
		t.Errorf("the due sync was not published: %+v", sender.sent)
	}
}

// One connection that cannot be published does not stop the others.
func TestOneSyncThatCannotBePublishedDoesNotStopTheRest(t *testing.T) {
	sender := &fakeSender{refuse: func(message produce.Message) bool {
		return message.Task == Task && message.Kwargs["connector_id"] == int64(7)
	}}
	tick := newTick(&fakeSchedule{due: []Due{{ConnectorID: 7, UserID: alice}, {ConnectorID: 9, UserID: bob}}}, sender,
		&fakeFence{taken: map[int64]bool{}}, time.Now())
	published, err := tick.PublishDue(t.Context())
	if published != 1 || err == nil || !strings.Contains(err.Error(), "connection 7") {
		t.Errorf("published %d (%v)", published, err)
	}
}
