package publicshare

import (
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

// A shared text loses every source chip, in either bracket style, and the
// space in front of it, and nothing else: not a link, not an ordinary
// bracket, not the indentation of a list.
func TestSharedTextHasNoSourceChips(t *testing.T) {
	for in, want := range map[string]string{
		"Tides come twice a day [citation:481].":               "Tides come twice a day.",
		"Twice\t【\u200bcitation:https://example.test/a】 a day": "Twice a day",
		"One[citation:1][citation:2], two":                     "One, two",
		"[a link](https://example.test) and [1] stay":          "[a link](https://example.test) and [1] stay",
		"- a\n  - nested [citation:9]\n":                       "- a\n  - nested\n",
		"An unclosed [citation:4\nnext line":                   "An unclosed [citation:4\nnext line",
	} {
		if got := withoutCitations(in); got != want {
			t.Errorf("%q became %q, want %q", in, got, want)
		}
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
	if len(Served()) != 4 || len(Public()) != 3 {
		t.Errorf("%d served, %d public", len(Served()), len(Public()))
	}
	for _, pair := range Public() {
		if pair != "GET "+PagePath && !strings.HasPrefix(pair, "GET /api/v1/public/{share_token}/") {
			t.Errorf("%s is public and is not a read under a share token", pair)
		}
	}
}
