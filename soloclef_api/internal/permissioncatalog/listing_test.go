package permissioncatalog

import (
	"os"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// testdata/catalogue.json is the body the Python tier returned for
// GET /api/v1/permissions, captured over HTTP. It carries no account's text —
// every string in it is a permission name or a description written in this
// repository — so it is committed as it came back.
func TestTheCatalogueIsThePythonTiersBytes(t *testing.T) {
	want, err := os.ReadFile("testdata/catalogue.json")
	if err != nil {
		t.Fatal(err)
	}
	got, err := pythonjson.Marshal(Listing())
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the catalogue is not the captured bytes\n got %d bytes\nwant %d bytes", len(got), len(want))
		for index := range min(len(got), len(want)) {
			if got[index] != want[index] {
				t.Fatalf("first difference at byte %d: got %q, want %q",
					index, excerpt(got, index), excerpt(want, index))
			}
		}
	}
}

// A permission the description dict does not name is described by its own
// value, which is why three groups of the catalogue read that way.
func TestAnUndescribedPermissionDescribesItself(t *testing.T) {
	if got := Describe("video_presentations:create"); got != "Permission for video_presentations:create" {
		t.Errorf("described as %q", got)
	}
	if got := Describe("documents:read"); got != "View and search documents in the workspace" {
		t.Errorf("described as %q", got)
	}
}

// The wildcard's value has no colon, so its category is the fallback.
func TestTheWildcardsCategory(t *testing.T) {
	if got := Category(FullAccess); got != GeneralCategory {
		t.Errorf("category %q, want %q", got, GeneralCategory)
	}
	if got := Category("members:manage_roles"); got != "members" {
		t.Errorf("category %q, want members", got)
	}
}

// Known is create_role's "Invalid permission" check, and it is the enum rather
// than a shape: a value that looks like a permission is not one.
func TestOnlyTheEnumsValuesAreKnown(t *testing.T) {
	for _, value := range []string{"documents:read", "*", "automations:execute"} {
		if !Known(value) {
			t.Errorf("%q is in the enum", value)
		}
	}
	for _, value := range []string{"documents:fly", "", "DOCUMENTS_READ", "documents"} {
		if Known(value) {
			t.Errorf("%q is not in the enum", value)
		}
	}
}

func excerpt(body []byte, at int) string {
	start := max(at-20, 0)
	end := min(at+20, len(body))
	return string(body[start:end])
}
