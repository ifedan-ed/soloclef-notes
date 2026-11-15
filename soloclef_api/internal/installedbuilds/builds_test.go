package installedbuilds

import (
	"strings"
	"testing"
)

// The snapshots load, each typed from the openapi.json its build used, and
// each calls the routes every Go-era build calls at launch: a snapshot that
// lost those would pin nothing.
func TestTheSnapshotsAreTheInstalledBuilds(t *testing.T) {
	builds := mustLoad(t)
	for _, build := range builds {
		calls := map[string]bool{}
		for _, call := range build.Calls {
			calls[call.Operation()] = true
		}
		for _, launch := range []string{"GET /users/me", "POST /auth/jwt/refresh", "GET /api/v1/notifications/unread-counts-batch", "GET /api/v1/notebooks"} {
			if !calls[launch] {
				t.Errorf("%s: calls.json lacks %s", build.Label(), launch)
			}
		}
		if len(build.Calls) < 100 {
			t.Errorf("%s: only %d calls read from its source", build.Label(), len(build.Calls))
		}
	}
}

// checked is CheckAnswer's problems.
func checked(problems []string, _ []int) []string { return problems }

// snapshot is one build's snapshot by its directory.
func snapshot(t *testing.T, dir string) Build {
	t.Helper()
	for _, build := range mustLoad(t) {
		if build.Dir == dir {
			return build
		}
	}
	t.Fatalf("no snapshot %s", dir)
	return Build{}
}

func call(t *testing.T, build Build, operation string) Call {
	t.Helper()
	for _, c := range build.Calls {
		if c.Operation() == operation {
			return c
		}
	}
	t.Fatalf("%s does not call %s", build.Label(), operation)
	return Call{}
}

// The answer that crashed 1.1.0 on 2026-11-13, and the one that fixed it,
// read against the snapshot itself.
func TestTheUnreadCountsWithoutAnnouncementsBreakTheBuild(t *testing.T) {
	build := snapshot(t, "ios-1.1.0-build4-731477d8a")
	batch := call(t, build, "GET /api/v1/notifications/unread-counts-batch")

	broken := checked(build.CheckAnswer(batch, 200, []byte(`{"status":{"total_unread":4,"recent_unread":2}}`)))
	if len(broken) != 1 || !strings.Contains(broken[0], "announcements") || !strings.Contains(broken[0], "missing") {
		t.Errorf("the answer without announcements: %q", broken)
	}
	fixed := checked(build.CheckAnswer(batch, 200, []byte(`{"status":{"total_unread":4,"recent_unread":2},"announcements":{"total_unread":0,"recent_unread":0}}`)))
	if len(fixed) != 0 {
		t.Errorf("the hot-fixed answer: %q", fixed)
	}
	if !build.RequiresField(batch, "announcements") || !build.RequiresField(batch, "announcements.total_unread") || build.RequiresField(batch, "news") {
		t.Error("RequiresField does not read the snapshot's fields")
	}
}

// What else counts as breaking, and what does not.
func TestAnAnswerIsReadAsTheBuildReadsIt(t *testing.T) {
	build := snapshot(t, "ios-1.1.0-build4-731477d8a")
	notebooks := call(t, build, "GET /api/v1/notebooks")
	me := call(t, build, "GET /users/me")
	for _, c := range []struct {
		name string
		call Call
		body string
		says []string
	}{
		{"an extra field is ignored", me, `{"id":"u","email":"a@b.c","credit_micros_balance":0,"display_name":null,"something_new":1}`, nil},
		{"a wrong type", me, `{"id":7,"email":"a@b.c","credit_micros_balance":0}`, []string{"id: is integer 7, the build reads string"}},
		{"null where the build does not allow it", me, `{"id":"u","email":null,"credit_micros_balance":0}`, []string{"email: is null"}},
		{"not JSON", me, `<html>`, []string{"not JSON"}},
		{"a list's item", notebooks, `[{"id":"one"}]`, []string{"[0].id: is string"}},
	} {
		got := strings.Join(checked(build.CheckAnswer(c.call, 200, []byte(c.body))), "\n")
		if len(c.says) == 0 && got != "" {
			t.Errorf("%s: %s", c.name, got)
		}
		for _, piece := range c.says {
			if !strings.Contains(got, piece) {
				t.Errorf("%s: %q does not say %q", c.name, got, piece)
			}
		}
	}
}

// The registry's needs are read from the snapshots: the announcements tab
// is needed because the builds send category=announcements.
func TestANeedIsReadFromWhatTheBuildSends(t *testing.T) {
	builds := mustLoad(t)
	for _, c := range []struct {
		need Need
		want bool
	}{
		{Need{Operation: "GET /api/v1/notifications", Query: [2]string{"category", "announcements"}}, true},
		{Need{Operation: "GET /api/v1/notifications", Query: [2]string{"category", "news"}}, false},
		{Need{Operation: "GET /api/v1/notifications/unread-counts-batch", Field: "announcements"}, true},
		{Need{Operation: "GET /api/v1/notifications/unread-counts-batch", Field: "news"}, false},
		{Need{Operation: "GET /api/v1/notebook-links"}, true},
		{Need{Operation: "GET /api/v1/nothing"}, false},
	} {
		if got := len(Answer{Needs: []Need{c.need}}.NeededBy(builds)) > 0; got != c.want {
			t.Errorf("%s: needed %v, want %v", c.need, got, c.want)
		}
	}
}

func TestShapeIgnoresParameterNames(t *testing.T) {
	if Shape("GET /api/v1/public/{token}/x") != Shape("GET /api/v1/public/{share_token}/x") || Shape("GET /a/{b}") == Shape("GET /a/b") {
		t.Error(Shape("GET /api/v1/public/{token}/x"))
	}
}
