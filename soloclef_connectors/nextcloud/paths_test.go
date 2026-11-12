package nextcloud

import (
	"testing"
)

func TestAPathIsMadeCanonicalOrRefused(t *testing.T) {
	for _, test := range []struct {
		name, raw, want string
		refused         bool
	}{
		{name: "the root", raw: "/", want: "/"},
		{name: "nothing is the root", raw: "", want: "/"},
		{name: "a plain file", raw: "/Notes/a b.pdf", want: "/Notes/a b.pdf"},
		{name: "no leading slash", raw: "Notes/a.pdf", want: "/Notes/a.pdf"},
		{name: "a trailing slash is dropped", raw: "/Notes/", want: "/Notes"},
		{name: "doubled slashes and dots collapse", raw: "//Notes/./2026//a.pdf", want: "/Notes/2026/a.pdf"},
		{name: "a name ending in a space keeps it", raw: "/Notes/draft ", want: "/Notes/draft "},
		{name: "a name that is only dots is a name", raw: "/Notes/...", want: "/Notes/..."},
		{name: "a percent sign is a character, not an escape", raw: "/100%25 done.pdf", want: "/100%25 done.pdf"},
		{name: "climbing out", raw: "/Notes/../../etc/passwd", refused: true},
		{name: "climbing out at the end", raw: "/Notes/..", refused: true},
		{name: "a NUL", raw: "/Notes/a\x00.pdf", refused: true},
		{name: "a backslash", raw: `/Notes\a.pdf`, refused: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizePath(test.raw)
			if test.refused {
				if err == nil {
					t.Fatalf("%q was accepted as %q", test.raw, got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Errorf("NormalizePath(%q) = %q, %v; want %q", test.raw, got, err, test.want)
			}
		})
	}
}

func TestTheURLOfAPathEscapesEachNameOnItsOwn(t *testing.T) {
	for path, want := range map[string]string{
		"/":                    "https://cloud.example/nc/remote.php/dav/files/ada@example.org/",
		"/Notes/a b.pdf":       "https://cloud.example/nc/remote.php/dav/files/ada@example.org/Notes/a%20b.pdf",
		"/Q&A/what? #1 50%.md": "https://cloud.example/nc/remote.php/dav/files/ada@example.org/Q&A/what%3F%20%231%2050%25.md",
		"/Über/naïve.txt":      "https://cloud.example/nc/remote.php/dav/files/ada@example.org/%C3%9Cber/na%C3%AFve.txt",
	} {
		// A base with a sub-path and a trailing slash. "@" and "&" are
		// legal in a URL path and are left as they are; what would end the
		// path ("?", "#") or be read as an escape ("%") is escaped.
		if got := davURL("https://cloud.example/nc/", "ada@example.org", path); got != want {
			t.Errorf("davURL(%q) = %s, want %s", path, got, want)
		}
	}
}

func TestAnHrefIsReadBackAsAPathInsideTheUsersFiles(t *testing.T) {
	for _, test := range []struct {
		name, href, want string
		outside          bool
	}{
		{name: "a bare path", href: "/remote.php/dav/files/ada/Notes/a%20b.pdf", want: "/Notes/a b.pdf"},
		{name: "a folder, with its trailing slash", href: "/remote.php/dav/files/ada/Notes/", want: "/Notes"},
		{name: "the root", href: "/remote.php/dav/files/ada/", want: "/"},
		{name: "the root without its slash", href: "/remote.php/dav/files/ada", want: "/"},
		{name: "a full URL under a sub-path", href: "https://cloud.example/nc/remote.php/dav/files/ada/a.pdf", want: "/a.pdf"},
		{name: "an escaped question mark stays in the name", href: "/remote.php/dav/files/ada/what%3F.md", want: "/what?.md"},
		{name: "another user's files", href: "/remote.php/dav/files/bob/a.pdf", outside: true},
		{name: "a user whose name only starts the same", href: "/remote.php/dav/files/adam/a.pdf", outside: true},
		{name: "not the files tree at all", href: "/remote.php/dav/calendars/ada/personal/", outside: true},
		{name: "an href that climbs", href: "/remote.php/dav/files/ada/../bob/a.pdf", outside: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, inside := pathFromHref(test.href, "ada")
			if inside == test.outside || got != test.want {
				t.Errorf("pathFromHref(%q) = %q, %v; want %q, outside=%v", test.href, got, inside, test.want, test.outside)
			}
		})
	}
	// The user segment may arrive escaped or not.
	for _, href := range []string{"/remote.php/dav/files/ada%40example.org/a.pdf", "/remote.php/dav/files/ada@example.org/a.pdf"} {
		if got, inside := pathFromHref(href, "ada@example.org"); !inside || got != "/a.pdf" {
			t.Errorf("pathFromHref(%q) = %q, %v", href, got, inside)
		}
	}
}
