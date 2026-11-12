package nextcloudimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// planOf plans picks against a stand-in Nextcloud, with the local parser
// (which reads .pdf, .docx and .epub; Go itself reads .md) and a per-file
// limit of 100 bytes.
func planOf(t *testing.T, files map[string]node, picks ...Pick) (Plan, error) {
	t.Helper()
	registry := parserchoicetest.Registry(t, parse.Local, parse.Datalab)
	chosen, refused := parserchoice.Choose(registry, "local")
	if refused != nil {
		t.Fatal(refused)
	}
	return planner{remote: newCloud(t, files).client(), parsers: registry, chosen: chosen, maxBytes: 100}.plan(context.Background(), picks)
}

func paths(files []PlannedFile) []string {
	listed := []string{}
	for _, file := range files {
		listed = append(listed, file.Entry.Path)
	}
	return listed
}

func TestAPickBecomesAPlan(t *testing.T) {
	files := map[string]node{
		"/Plans":                 {folder: true},
		"/Plans/a.pdf":           {content: "a"},
		"/Plans/notes.md":        {content: "# n"},
		"/Plans/photo.png":       {content: "png"},
		"/Plans/huge.pdf":        {size: 101},
		"/Plans/exactly.pdf":     {size: 100},
		"/Plans/unsized.pdf":     {content: "?", noSize: true},
		"/Plans/Old":             {folder: true},
		"/Plans/Old/2025.pdf":    {content: "old"},
		"/Plans/Old/slides.pptx": {content: "deck"},
		"/loose.pdf":             {content: "loose"},
		"/README":                {content: "no extension"},
	}
	plan, err := planOf(t, files,
		Pick{Path: "/Plans", Folder: true},
		Pick{Path: "/loose.pdf"},
		Pick{Path: "/README"},
		// The same things again, spelled differently: counted once.
		Pick{Path: "Plans/", Folder: true},
		Pick{Path: "/Plans/a.pdf"},
	)
	if err != nil {
		t.Fatal(err)
	}

	// What the local parser reads, and the text Go reads itself. A file of
	// exactly the limit is in; one whose size the server left out is in
	// too, and is measured when it downloads.
	want := []string{"/Plans/a.pdf", "/Plans/exactly.pdf", "/Plans/notes.md", "/Plans/unsized.pdf", "/Plans/Old/2025.pdf", "/loose.pdf"}
	if got := paths(plan.Files); !reflect.DeepEqual(got, want) {
		t.Errorf("planned\n%v\nwant\n%v", got, want)
	}
	if want := []string{"/Plans/photo.png", "/Plans/Old/slides.pptx", "/README"}; !reflect.DeepEqual(plan.Unsupported, want) {
		t.Errorf("unsupported %v, want %v", plan.Unsupported, want)
	}
	if !reflect.DeepEqual(plan.TooLarge, []string{"/Plans/huge.pdf"}) {
		t.Errorf("too large %v", plan.TooLarge)
	}
	if plan.OverLimit {
		t.Error("a small pick was called too big")
	}
	if !reflect.DeepEqual(plan.PickedFolders, []PickedFolder{{Path: "/Plans", Name: "Plans"}}) {
		t.Errorf("picked folders %v", plan.PickedFolders)
	}
	// 1 + 100 + 3 + (unsized: nothing) + 3 + 5.
	if plan.TotalBytes() != 112 {
		t.Errorf("total %d bytes", plan.TotalBytes())
	}

	// Where each goes: a file from the picked folder under that folder's
	// name and its sub-folders; a file picked on its own, nowhere.
	for _, file := range plan.Files {
		switch file.Entry.Path {
		case "/Plans/Old/2025.pdf":
			if !reflect.DeepEqual(file.Folders, []string{"Plans", "Old"}) || file.PickedFolder != "/Plans" {
				t.Errorf("2025.pdf goes in %v, from %q", file.Folders, file.PickedFolder)
			}
		case "/loose.pdf":
			if len(file.Folders) != 0 || file.PickedFolder != "" {
				t.Errorf("loose.pdf goes in %v, from %q", file.Folders, file.PickedFolder)
			}
		case "/Plans/a.pdf":
			// Picked inside its folder first, so it belongs to the folder.
			if file.PickedFolder != "/Plans" {
				t.Errorf("a.pdf, picked twice, was planned as a loose file")
			}
		}
	}
}

func TestAnotherParserReadsOtherFiles(t *testing.T) {
	registry := parserchoicetest.Registry(t, parse.Local, parse.Datalab)
	chosen, _ := parserchoice.Choose(registry, "datalab")
	cloud := newCloud(t, map[string]node{"/photo.png": {content: "png"}, "/book.epub": {content: "epub"}})
	plan, err := planner{remote: cloud.client(), parsers: registry, chosen: chosen, maxBytes: 100}.
		plan(context.Background(), []Pick{{Path: "/photo.png"}, {Path: "/book.epub"}})
	if err != nil {
		t.Fatal(err)
	}
	// Datalab reads .png and not .epub: the opposite of the local parser.
	if !reflect.DeepEqual(paths(plan.Files), []string{"/photo.png"}) || !reflect.DeepEqual(plan.Unsupported, []string{"/book.epub"}) {
		t.Errorf("planned %v, unsupported %v", paths(plan.Files), plan.Unsupported)
	}
}

func TestAPickThatIsTooBigIsCalledThat(t *testing.T) {
	files := map[string]node{"/Big": {folder: true}}
	for index := 0; index < MaxImportFiles+1; index++ {
		files["/Big/"+strings.Repeat("0", 3-len(itoa(index)))+itoa(index)+".pdf"] = node{content: "x"}
	}
	plan, err := planOf(t, files, Pick{Path: "/Big", Folder: true})
	if err != nil {
		t.Fatal(err)
	}
	if !plan.OverLimit || len(plan.Files) != MaxImportFiles {
		t.Errorf("over limit %v with %d files planned", plan.OverLimit, len(plan.Files))
	}
}

func itoa(number int) string {
	if number == 0 {
		return "0"
	}
	digits := ""
	for ; number > 0; number /= 10 {
		digits = string(rune('0'+number%10)) + digits
	}
	return digits
}

func TestPicksThatCannotBeImportedAsTheyStand(t *testing.T) {
	files := map[string]node{"/Plans": {folder: true}, "/a.pdf": {content: "a"}}
	for name, test := range map[string]struct {
		pick Pick
		says string
	}{
		"the whole drive":           {Pick{Path: "/", Folder: true}, "not the whole drive"},
		"a folder picked as a file": {Pick{Path: "/Plans"}, "Plans is a folder, not a file"},
		"a path that climbs":        {Pick{Path: "/Plans/../../etc"}, "is not a path inside your Nextcloud files"},
	} {
		_, err := planOf(t, files, test.pick)
		picked, isSelection := isSelection(err)
		if !isSelection || !strings.Contains(picked.message, test.says) {
			t.Errorf("%s: %v, want a refusal saying %q", name, err, test.says)
		}
	}
}

func TestTheDecisionForAFileSeenBefore(t *testing.T) {
	for _, test := range []struct {
		name  string
		prior *priorImport
		etag  string
		want  disposition
	}{
		{"never imported", nil, "e1", create},
		{"ready and unchanged", &priorImport{State: "ready", ETag: "e1"}, "e1", reuse},
		{"ready, changed in Nextcloud", &priorImport{State: "ready", ETag: "e1"}, "e2", requeue},
		{"ready, and the server sends no etag: not known to be the same", &priorImport{State: "ready", ETag: ""}, "", requeue},
		{"failed, even if unchanged", &priorImport{State: "failed", ETag: "e1"}, "e1", requeue},
		{"being indexed", &priorImport{State: "processing", ETag: "e1"}, "e2", reuse},
		{"stored and waiting for the indexer, however long", &priorImport{State: "pending", Stale: true}, "e1", reuse},
		{"being downloaded now", &priorImport{State: "pending", Stage: "downloading"}, "e1", reuse},
		{"a download abandoned by a process that died", &priorImport{State: "pending", Stage: "downloading", Stale: true}, "e1", requeue},
		{"being deleted", &priorImport{State: "deleting", ETag: "e1"}, "e1", beingDeleted},
	} {
		if got := decideImport(test.prior, test.etag); got != test.want {
			t.Errorf("%s: decided %d, want %d", test.name, got, test.want)
		}
	}
}

func TestTheIdentityIsTheOneEarlierImportsWereRecordedUnder(t *testing.T) {
	account := Account{BaseURL: "https://Cloud.Example/nc", Username: "ada"}
	// The document type, a source id of
	// nextcloud://{user}@{host, lower case}{path}, and the owner's id. The
	// sub-path of the server is not part of it, and never was.
	const want = "FILE:nextcloud://ada@cloud.example/Plans/a.pdf:" + owner
	got := identity(account, "/Plans/a.pdf", owner)
	if got != sha256Hex(want) {
		t.Errorf("identity %s is not sha256 of %q", got, want)
	}
	if identity(account, "/Plans/a.pdf", somebody) == got || identity(account, "/Plans/b.pdf", owner) == got {
		t.Error("the identity does not depend on the owner or the path")
	}
}

func sha256Hex(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}
