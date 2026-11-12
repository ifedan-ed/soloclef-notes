package filekind

import (
	"sort"
	"testing"
)

func TestExtensionIsLowerCaseWithItsDot(t *testing.T) {
	cases := map[string]string{
		"Report.PDF":         ".pdf",
		"notes/2026/plan.md": ".md",
		"archive.tar.gz":     ".gz",
		"README":             "",
		".gitignore":         ".gitignore",
	}
	for name, want := range cases {
		if got := Extension(name); got != want {
			t.Errorf("Extension(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestKindByExtension(t *testing.T) {
	cases := map[string]Kind{
		".md": Text, ".txt": Text, ".go": Text,
		".csv": CommaSeparated, ".tsv": TabSeparated,
		".html": HTML, ".htm": HTML, ".xhtml": HTML,
		".pdf": Document, ".docx": Document,
		// Unknown extensions are the chosen parser's to accept or refuse.
		".xyz": Document, "": Document,
	}
	for extension, want := range cases {
		if got := Of(extension); got != want {
			t.Errorf("Of(%q) = %v, want %v", extension, got, want)
		}
	}
}

func TestReadInGoIsSortedAndACopy(t *testing.T) {
	list := ReadInGo()
	if !sort.StringsAreSorted(list) {
		t.Fatal("not sorted")
	}
	list[0] = "changed"
	if ReadInGo()[0] == "changed" {
		t.Fatal("the caller can change the package's list")
	}
}

func TestMIME(t *testing.T) {
	if got := MIME(".pdf"); got != "application/pdf" {
		t.Errorf("pdf = %q", got)
	}
	if got := MIME(".hwp"); got != "application/octet-stream" {
		t.Errorf("unknown = %q", got)
	}
}

func TestExtensionOfIsTheTableTheOtherWayRound(t *testing.T) {
	for mediaType, want := range map[string]string{
		"application/pdf":            ".pdf",
		"Application/PDF; name=scan": ".pdf",
		"image/jpeg":                 ".jpg",
		"image/tiff":                 ".tif",
		"application/vnd.openxmlformats-officedocument.wordprocessingml.document": ".docx",
		"application/x-unheard-of": "",
	} {
		if got := ExtensionOf(mediaType); got != want {
			t.Errorf("ExtensionOf(%q) = %q, want %q", mediaType, got, want)
		}
	}
}
