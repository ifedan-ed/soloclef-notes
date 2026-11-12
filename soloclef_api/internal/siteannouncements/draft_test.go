package siteannouncements

import (
	"strings"
	"testing"
	"time"
)

func TestADraftIsCheckedFieldByField(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	refused := func(body string) []string {
		_, failures := readDraft([]byte(body), now)
		var fields []string
		for _, failure := range failures {
			fields = append(fields, strings.Join(failure.Location, "."))
		}
		return fields
	}

	checked, failures := readDraft([]byte(`{"title":"  Planned \n maintenance ","body":"\nTonight at ten.\n","link":" /dashboard/settings ","expires_at":"2026-11-13T09:00:00+01:00"}`), now)
	if len(failures) != 0 {
		t.Fatalf("a good draft was refused: %v", failures)
	}
	if checked.Title != "Planned maintenance" || checked.Body != "Tonight at ten." || *checked.Link != "/dashboard/settings" {
		t.Errorf("read %+v", checked)
	}
	if !checked.ExpiresAt.Equal(time.Date(2026, 11, 13, 8, 0, 0, 0, time.UTC)) || checked.ExpiresAt.Location() != time.UTC {
		t.Errorf("the expiry reads %v", checked.ExpiresAt)
	}
	// No link and no expiry, said as null or as nothing, are none.
	checked, failures = readDraft([]byte(`{"title":"t","body":"b","link":null,"expires_at":""}`), now)
	if len(failures) != 0 || checked.Link != nil || checked.ExpiresAt != nil {
		t.Errorf("read %+v, refused %v", checked, failures)
	}

	for _, c := range []struct {
		name, body string
		want       string
	}{
		{"no title", `{"body":"b"}`, "body.title"},
		{"a title of spaces", `{"title":"   ","body":"b"}`, "body.title"},
		{"a long title", `{"title":"` + strings.Repeat("é", 121) + `","body":"b"}`, "body.title"},
		{"no body", `{"title":"t","body":" "}`, "body.body"},
		{"a long body", `{"title":"t","body":"` + strings.Repeat("x", 2001) + `"}`, "body.body"},
		{"an expiry already past", `{"title":"t","body":"b","expires_at":"2026-11-12T09:00:00Z"}`, "body.expires_at"},
		{"an expiry that is not a time", `{"title":"t","body":"b","expires_at":"next week"}`, "body.expires_at"},
		{"not an object", `["t","b"]`, "body"},
		{"both missing", `{}`, "body.title,body.body"},
	} {
		if got := strings.Join(refused(c.body), ","); got != c.want {
			t.Errorf("%s: refused %q, want %q", c.name, got, c.want)
		}
	}
	// Characters count, not bytes: 120 accented letters are a title.
	if got := refused(`{"title":"` + strings.Repeat("é", 120) + `","body":"b"}`); len(got) != 0 {
		t.Errorf("a 120-character title was refused: %v", got)
	}
}

func TestOnlyALinkThatCannotRunIsStored(t *testing.T) {
	for link, want := range map[string]bool{
		"/dashboard/settings":           true,
		"/":                             true,
		"https://example.test/notes":    true,
		"HTTP://example.test":           true,
		"//evil.example":                false,
		`/\evil.example`:                false,
		"javascript:alert(1)":           false,
		"data:text/html,x":              false,
		"example.test":                  false,
		"https://example.test/a b":      false,
		"https://example.test/a\nb":     false,
		"/" + strings.Repeat("a", 2048): false,
	} {
		if got := linkAllowed(link); got != want {
			t.Errorf("%q: allowed %v, want %v", link, got, want)
		}
	}
}

func TestStatusSaysWhyAnAnnouncementStoppedShowing(t *testing.T) {
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	past, future := now.Add(-time.Hour), now.Add(time.Hour)
	for _, c := range []struct {
		withdrawn, expires *time.Time
		want               string
	}{
		{nil, nil, "live"},
		{nil, &future, "live"},
		{nil, &now, "expired"},
		{nil, &past, "expired"},
		{&past, &past, "withdrawn"},
		{&past, nil, "withdrawn"},
	} {
		if got := statusAt(c.withdrawn, c.expires, now); got != c.want {
			t.Errorf("withdrawn %v, expires %v: %s, want %s", c.withdrawn, c.expires, got, c.want)
		}
	}
}
