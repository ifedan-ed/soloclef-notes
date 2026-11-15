package appupdate

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/appversion"
	"git.danvics.com/danvics/soloclef/soloclef_notify/notify"
	"git.danvics.com/danvics/soloclef/soloclef_notify/pushpolicy"
)

const bundle = "com.danvics.soloclef"

func version(t *testing.T, text string) appversion.Version {
	t.Helper()
	v, err := appversion.Parse(text)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// stubApple stands in for Apple's lookup: it answers body with status, and
// records the bundle ids asked about.
func stubApple(t *testing.T, status int, body string) (AppStore, *[]string) {
	t.Helper()
	var asked []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.Query().Get("bundleId"))
		// Apple's own type for this answer.
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return AppStore{BaseURL: server.URL, BundleID: bundle, HTTP: &http.Client{Timeout: 5 * time.Second}}, &asked
}

// The lookup reads one plain listing of this app; every other answer is an
// error saying what it was, never a version guessed from it.
func TestTheLookupReadsOnlyAPlainListingOfThisApp(t *testing.T) {
	store, asked := stubApple(t, http.StatusOK,
		`{"resultCount":1,"results":[{"bundleId":"com.danvics.soloclef","version":"1.2.0","trackId":6821393434,"trackViewUrl":"https://apps.apple.com/us/app/soloclef/id6821393434?uo=4"}]}`)
	release, err := store.Lookup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if release.Version.String() != "1.2.0" || release.TrackID != 6821393434 {
		t.Errorf("release = %+v", release)
	}
	// No storefront in the address: Apple sends each person to their own.
	if release.StoreURL() != "https://apps.apple.com/app/id6821393434" {
		t.Errorf("store URL %q", release.StoreURL())
	}
	if len(*asked) != 1 || (*asked)[0] != bundle {
		t.Errorf("asked about %v", *asked)
	}

	for name, tc := range map[string]struct {
		status int
		body   string
		says   string
	}{
		"not listed":       {200, `{"resultCount":0,"results":[]}`, "lists no app"},
		"another app":      {200, `{"resultCount":1,"results":[{"bundleId":"com.other.app","version":"1.2.0","trackId":1}]}`, "com.other.app"},
		"a tagged version": {200, `{"resultCount":1,"results":[{"bundleId":"com.danvics.soloclef","version":"1.2.0-beta","trackId":1}]}`, "1.2.0-beta"},
		"no app id":        {200, `{"resultCount":1,"results":[{"bundleId":"com.danvics.soloclef","version":"1.2.0"}]}`, "no app id"},
		"not JSON":         {200, `<html>`, "does not read"},
		"Apple down":       {503, ``, "503"},
	} {
		store, _ := stubApple(t, tc.status, tc.body)
		if _, err := store.Lookup(context.Background()); err == nil || !strings.Contains(err.Error(), tc.says) {
			t.Errorf("%s: %v", name, err)
		}
	}
	unlisted, _ := stubApple(t, 200, `{"resultCount":0,"results":[]}`)
	if _, err := unlisted.Lookup(context.Background()); !errors.Is(err, ErrNotListed) {
		t.Errorf("an empty answer is %v, want ErrNotListed", err)
	}
}

// fakeStore is Store with a fixed answer.
type fakeStore struct {
	release Release
	err     error
	asked   int
}

func (f *fakeStore) Lookup(context.Context) (Release, error) {
	f.asked++
	return f.release, f.err
}

// fakeRows is Rows in memory.
type fakeRows struct {
	kept        *Kept
	phones      []Phone
	preferences map[string]pushpolicy.Stored
}

func (f *fakeRows) Kept(context.Context, string) (Kept, bool, error) {
	if f.kept == nil {
		return Kept{}, false, nil
	}
	return *f.kept, true, nil
}

func (f *fakeRows) Keep(_ context.Context, _ string, release Release, at time.Time) error {
	f.kept = &Kept{Release: release, CheckedAt: at}
	return nil
}

func (f *fakeRows) Answered(_ context.Context, _ string, at time.Time) error {
	f.kept.CheckedAt = at
	return nil
}

func (f *fakeRows) Untold(_ context.Context, version string, _ time.Time) ([]Phone, error) {
	var out []Phone
	for _, p := range f.phones {
		if p.AppVersion != version {
			out = append(out, p)
		}
	}
	return out, nil
}

func (f *fakeRows) Preferences(context.Context, []string) (map[string]pushpolicy.Stored, error) {
	return f.preferences, nil
}

// fakeNotifier records each account told.
type fakeNotifier struct {
	told []notify.AppUpdate
}

func (f *fakeNotifier) AppUpdated(_ context.Context, update notify.AppUpdate) ([]int64, error) {
	f.told = append(f.told, update)
	return update.Devices, nil
}

func newJob(t *testing.T, store *fakeStore, rows *fakeRows, now time.Time) (*Job, *fakeNotifier) {
	t.Helper()
	notifier := &fakeNotifier{}
	job := &Job{
		BundleID: bundle, Store: store, Rows: rows, Notifier: notifier,
		Timing: Timing{Every: time.Hour, LookupEvery: 24 * time.Hour, Accounts: 100},
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:    func() time.Time { return now },
	}
	return job, notifier
}

var noon = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

// Only the phones on an older version are told, grouped by account: not a
// phone on the store's version written another way ("1.0.0" for "1.0"),
// nor a newer one (a TestFlight build).
func TestOnlyPhonesOnAnOlderVersionAreTold(t *testing.T) {
	rows := &fakeRows{
		kept: &Kept{Release: Release{Version: version(t, "1.2"), TrackID: 9}, CheckedAt: noon},
		phones: []Phone{
			{ID: 1, UserID: "ada", AppVersion: "1.1.0"},
			{ID: 2, UserID: "ada", AppVersion: "1.2.0"},
			{ID: 3, UserID: "ada", AppVersion: "1.0"},
			{ID: 4, UserID: "bob", AppVersion: "1.3.0"},
			{ID: 5, UserID: "cy", AppVersion: "1.10"},
			{ID: 6, UserID: "dee", AppVersion: "0.9.9"},
		},
	}
	job, notifier := newJob(t, &fakeStore{}, rows, noon)
	if err := job.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notifier.told) != 2 {
		t.Fatalf("told %+v", notifier.told)
	}
	ada, dee := notifier.told[0], notifier.told[1]
	if ada.UserID != "ada" || len(ada.Devices) != 2 || ada.Devices[0] != 1 || ada.Devices[1] != 3 {
		t.Errorf("ada was told %+v", ada)
	}
	if dee.UserID != "dee" || len(dee.Devices) != 1 || dee.Devices[0] != 6 {
		t.Errorf("dee was told %+v", dee)
	}
	if ada.Version != "1.2" || ada.StoreURL != "https://apps.apple.com/app/id9" {
		t.Errorf("the update said %+v", ada)
	}
}

// An account inside its quiet hours is not told, and so not marked: the
// next pass after them tells it. An account with the kind switched off is
// told (its inbox row is written; the delivery pushes nothing), and so is
// one whose quiet hours are over.
func TestQuietHoursHoldTheUpdateForALaterPass(t *testing.T) {
	start, end := int16(22*60), int16(7*60)
	zone := "Africa/Lagos"
	rows := &fakeRows{
		kept: &Kept{Release: Release{Version: version(t, "1.2.0"), TrackID: 9}},
		phones: []Phone{
			{ID: 1, UserID: "sleeping", AppVersion: "1.1.0"},
			{ID: 2, UserID: "switched-off", AppVersion: "1.1.0"},
			{ID: 3, UserID: "awake", AppVersion: "1.1.0"},
		},
		preferences: map[string]pushpolicy.Stored{
			"sleeping":     {Choices: []byte(`{}`), QuietStartMinute: &start, QuietEndMinute: &end, TimeZone: &zone},
			"switched-off": {Choices: []byte(`{"update": false}`), QuietStartMinute: &start, QuietEndMinute: &end, TimeZone: &zone},
		},
	}
	// 23:30 in Lagos.
	night := time.Date(2026, 11, 13, 22, 30, 0, 0, time.UTC)
	rows.kept.CheckedAt = night
	job, notifier := newJob(t, &fakeStore{}, rows, night)
	if err := job.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, update := range notifier.told {
		names = append(names, update.UserID)
	}
	if strings.Join(names, ",") != "switched-off,awake" {
		t.Errorf("told at night: %v", names)
	}

	// 08:00 in Lagos: the sleeper's quiet hours are over.
	morning := time.Date(2026, 11, 14, 7, 0, 0, 0, time.UTC)
	rows.phones = rows.phones[:1]
	job, notifier = newJob(t, &fakeStore{}, rows, morning)
	if err := job.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notifier.told) != 1 || notifier.told[0].UserID != "sleeping" {
		t.Errorf("told in the morning: %+v", notifier.told)
	}
}

// Apple is asked when nothing is kept or the last answer is a day old, and
// not otherwise. A newer answer is kept; an older one (Apple's lookup
// lagging after a release) never moves the version back.
func TestAppleIsAskedOnceADayAndNeverMovesTheVersionBack(t *testing.T) {
	store := &fakeStore{release: Release{Version: version(t, "1.2.0"), TrackID: 9}}
	rows := &fakeRows{}
	job, _ := newJob(t, store, rows, noon)
	if err := job.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.asked != 1 || rows.kept == nil || rows.kept.Release.Version.String() != "1.2.0" {
		t.Fatalf("asked %d, kept %+v", store.asked, rows.kept)
	}

	job.Now = func() time.Time { return noon.Add(23 * time.Hour) }
	if err := job.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.asked != 1 {
		t.Errorf("Apple was asked again within the day")
	}

	store.release = Release{Version: version(t, "1.1.0"), TrackID: 9}
	job.Now = func() time.Time { return noon.Add(24 * time.Hour) }
	if err := job.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.asked != 2 || rows.kept.Release.Version.String() != "1.2.0" {
		t.Errorf("asked %d; the kept version went to %s", store.asked, rows.kept.Release.Version)
	}
	if !rows.kept.CheckedAt.Equal(noon.Add(24 * time.Hour)) {
		t.Errorf("the answer's time was not noted: %v", rows.kept.CheckedAt)
	}

	store.release = Release{Version: version(t, "1.3.0"), TrackID: 9}
	job.Now = func() time.Time { return noon.Add(48 * time.Hour) }
	if err := job.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if rows.kept.Release.Version.String() != "1.3.0" {
		t.Errorf("a newer version was not kept: %s", rows.kept.Release.Version)
	}
}

// A lookup that fails still tells the phones about the version already
// kept, and says it failed; with nothing kept, nobody is told anything.
func TestAFailedLookupStillTellsTheKeptVersion(t *testing.T) {
	store := &fakeStore{err: errors.New("the lookup timed out")}
	rows := &fakeRows{
		kept:   &Kept{Release: Release{Version: version(t, "1.2.0"), TrackID: 9}, CheckedAt: noon.Add(-48 * time.Hour)},
		phones: []Phone{{ID: 1, UserID: "ada", AppVersion: "1.1.0"}},
	}
	job, notifier := newJob(t, store, rows, noon)
	if err := job.Pass(context.Background()); err == nil || !strings.Contains(err.Error(), "the lookup timed out") {
		t.Errorf("the failure was not said: %v", err)
	}
	if len(notifier.told) != 1 {
		t.Errorf("told %+v", notifier.told)
	}

	empty := &fakeRows{phones: rows.phones}
	job, notifier = newJob(t, store, empty, noon)
	if err := job.Pass(context.Background()); err == nil {
		t.Error("a pass with no version at all said nothing")
	}
	if len(notifier.told) != 0 {
		t.Errorf("told with no version: %+v", notifier.told)
	}
}

// A pass tells at most Timing.Accounts accounts; the rest are the next
// pass's (they are not marked, so it finds them).
func TestAPassTellsABoundedNumberOfAccounts(t *testing.T) {
	rows := &fakeRows{kept: &Kept{Release: Release{Version: version(t, "2.0"), TrackID: 9}, CheckedAt: noon}}
	for i, user := range []string{"a", "b", "c"} {
		rows.phones = append(rows.phones, Phone{ID: int64(i + 1), UserID: user, AppVersion: "1.0"})
	}
	job, notifier := newJob(t, &fakeStore{}, rows, noon)
	job.Timing.Accounts = 2
	if err := job.Pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notifier.told) != 2 {
		t.Errorf("told %d accounts, want 2", len(notifier.told))
	}
}

// The setting is a bundle id or empty; nothing else is read as one.
func TestTheBundleIDSetting(t *testing.T) {
	for value, want := range map[string]string{"com.danvics.soloclef": "com.danvics.soloclef", " com.danvics.soloclef ": "com.danvics.soloclef", "": ""} {
		got, err := LoadBundleID(func(string) string { return value })
		if err != nil || got != want {
			t.Errorf("%q: %q, %v", value, got, err)
		}
	}
	for _, bad := range []string{"soloclef", "com.danvics/soloclef", "com danvics", "https://apps.apple.com"} {
		if _, err := LoadBundleID(func(string) string { return bad }); err == nil {
			t.Errorf("%q was taken as a bundle id", bad)
		}
	}
}
