package publicshare

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestOnlyWhatABrowserCannotRunIsShownInline(t *testing.T) {
	for mimeType, want := range map[string]string{
		"image/png":                "image",
		"IMAGE/JPEG":               "image",
		"image/svg+xml":            "file", // SVG carries script
		"application/pdf":          "pdf",
		"audio/mpeg":               "audio",
		"video/mp4; codecs=avc1":   "video",
		"text/html":                "file",
		"text/html; charset=utf-8": "file",
		"application/json":         "file",
		"":                         "file",
		"application/octet-stream": "file",
		"application/vnd.ms-excel": "file",
	} {
		if got := displayFor(mimeType); got != want {
			t.Errorf("%q is shown as %q, want %q", mimeType, got, want)
		}
		if shownInline(mimeType) != (want != "file") {
			t.Errorf("%q: inline disagrees with its display", mimeType)
		}
	}
}

func TestAnExpiryIsOneOfTheDialogsChoices(t *testing.T) {
	for body, want := range map[string]struct {
		days    int
		expires bool
		ok      bool
	}{
		``:                         {7, true, true},
		`  `:                       {7, true, true},
		`{}`:                       {7, true, true},
		`{"regenerate":true}`:      {7, true, true},
		`{"expires_in_days":7}`:    {7, true, true},
		`{"expires_in_days": 30 }`: {30, true, true},
		`{"expires_in_days":null}`: {0, false, true},
		`{"expires_in_days":14}`:   {0, false, false},
		`{"expires_in_days":"7"}`:  {0, false, false},
		`{"expires_in_days":7.0}`:  {0, false, false},
		`[7]`:                      {0, false, false},
		`{"expires_in_days":7`:     {0, false, false},
		strings.Repeat(" ", 5<<10): {0, false, false}, // larger than any honest body
	} {
		days, expires, ok := readExpiry(strings.NewReader(body))
		if days != want.days || expires != want.expires || ok != want.ok {
			t.Errorf("%.40q: %d %v %v, want %+v", body, days, expires, ok, want)
		}
	}
}

func TestStoredListsAreReadStrictly(t *testing.T) {
	list := entries([]byte(`[{"original_id":7,"a":1}, "text", null, {"original_id":"7"}, {"original_id":7.5}, {"original_id":true}, {"original_id":8}]`))
	if len(list) != 5 {
		t.Fatalf("only objects are entries: %v", list)
	}
	if found := numbered(list, "original_id", 7); found == nil || found["a"] == nil {
		t.Errorf("the entry numbered 7: %v", found)
	}
	// Text, a fraction and a truth are not the number 7 or 1.
	if numbered(list[1:], "original_id", 7) != nil || numbered(list, "original_id", 1) != nil {
		t.Error("a value that is not a whole number matched an id")
	}
	for _, column := range []string{``, `null`, `{"original_id":7}`, `"x"`, `[`} {
		if got := entries([]byte(column)); len(got) != 0 {
			t.Errorf("%q has entries: %v", column, got)
		}
	}
}

func TestInternalKeysAreRemovedAtAnyDepth(t *testing.T) {
	cleaned := withoutInternalKeys(map[string]any{
		"title": "One", "audio_file": "/srv/a.mp3", "user_id": "u",
		"layers": []any{map[string]any{"storage_key": "k", "text": "kept", "inner": map[string]any{"email": "e", "n": 1}}},
	})
	want := map[string]any{
		"title":  "One",
		"layers": []any{map[string]any{"text": "kept", "inner": map[string]any{"n": 1}}},
	}
	if !reflect.DeepEqual(cleaned, want) {
		t.Errorf("%v", cleaned)
	}
}

func TestAHalfConfiguredDeploymentIsRefusedAtStartUp(t *testing.T) {
	if err := (Routes{}).Check(); err == nil {
		t.Error("routes with nothing configured passed the check")
	}
	if err := (Routes{StallTimeout: time.Minute, FrontendURL: "https://x.test"}).Check(); err == nil {
		t.Error("routes with no pool passed the check")
	}
	if got := (Routes{FrontendURL: "https://x.test//"}).publicURL("tok"); got != "https://x.test/public/tok" {
		t.Errorf("a link's address: %s", got)
	}
	if len(Served()) != 8 || len(Public()) != 6 {
		t.Errorf("%d served, %d public", len(Served()), len(Public()))
	}
	for _, pair := range Public() {
		if !strings.HasPrefix(pair, "GET /api/v1/public/{share_token}/") {
			t.Errorf("%s is public and is not a read under a share token", pair)
		}
	}
}
