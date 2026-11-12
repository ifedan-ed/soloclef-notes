package nextcloudimport

import (
	"reflect"
	"strings"
	"testing"
)

func TestTheFolderChainOfAFileUnderAPickedFolder(t *testing.T) {
	for _, test := range []struct {
		picked, file string
		want         []string
	}{
		{"/Work/Plans", "/Work/Plans/a.pdf", []string{"Plans"}},
		{"/Work/Plans", "/Work/Plans/2026/Q1/a.pdf", []string{"Plans", "2026", "Q1"}},
		{"/Plans", "/Plans/a.pdf", []string{"Plans"}},
		// Deeper than the folder tree allows: the ninth level and below fold
		// into the eighth.
		{"/a", "/a/b/c/d/e/f/g/h/i/j/file.pdf", []string{"a", "b", "c", "d", "e", "f", "g", "h"}},
	} {
		if got := folderChain(test.picked, test.file); !reflect.DeepEqual(got, test.want) {
			t.Errorf("folderChain(%q, %q) = %v, want %v", test.picked, test.file, got, test.want)
		}
	}
}

func TestAFolderNameAFolderCanCarry(t *testing.T) {
	for raw, want := range map[string]string{
		"Plans":             "Plans",
		"  Plans \t 2026 ":  "Plans 2026",
		`a/b\c:d*e?f"g<h>|`: "a_b_c_d_e_f_g_h_",
		"":                  "folder",
		"..":                "folder",
		"///":               "_",
	} {
		if got := folderName(raw); got != want {
			t.Errorf("folderName(%q) = %q, want %q", raw, got, want)
		}
	}
	// A very long name is cut to 180 characters, and one whose characters
	// are wide to 255 bytes, on a character boundary either way.
	if got := folderName(strings.Repeat("a", 400)); got != strings.Repeat("a", 180) {
		t.Errorf("a long plain name became %d characters", len(got))
	}
	if got := folderName(strings.Repeat("é", 400)); got != strings.Repeat("é", 127) {
		t.Errorf("a long accented name became %d bytes", len(got))
	}
}
