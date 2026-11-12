package uploads

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/uploadvision"
)

// tree is the folder upload's database for one test: the documents the
// upload tests use, and each account's folders by their path from the top.
// Like rows, it decides with the route's own decide, so only the SQL is
// stood in for (the SQL runs against Postgres in database_test.go).
type tree struct {
	*rows
	nextID int64
	// byPath is folder ids by "{user_id}:name/name/…".
	byPath map[string]int64
	// watched is the parser a top folder was marked with.
	watched map[int64]string
	// recordErr fails the transaction.
	recordErr error
}

func (f *tree) FolderIn(_ context.Context, folderID int64, userID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pathOf(folderID, userID) != "", nil
}

// pathOf is the path a folder id was filed under in an account, or "": a
// folder of another account is not found, as the statement does not find it.
func (f *tree) pathOf(folderID int64, userID string) string {
	prefix := userID + ":"
	for path, id := range f.byPath {
		if id == folderID && strings.HasPrefix(path, prefix) {
			return path
		}
	}
	return ""
}

func (f *tree) ensure(path string) int64 {
	if id, found := f.byPath[path]; found {
		return id
	}
	f.nextID++
	f.byPath[path] = f.nextID
	return f.nextID
}

func (f *tree) RecordFolder(_ context.Context, batch FolderBatch) (FolderRecorded, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.recordErr != nil {
		return FolderRecorded{}, f.recordErr
	}
	// Decided for the whole batch before anything is written, as a
	// transaction would have it.
	recorded := FolderRecorded{Files: make([]Recorded, len(batch.Files))}
	for index, file := range batch.Files {
		prior := f.prior(batch.UserID, file.Identity)
		outcome := decide(prior, file.SHA256)
		if outcome == BeingDeleted {
			return FolderRecorded{}, &ErrBeingDeleted{Name: file.Name}
		}
		recorded.Files[index] = Recorded{DocumentID: file.DocumentID, Outcome: outcome}
		if prior != nil {
			recorded.Files[index].DocumentID = prior.ID
		}
	}

	var rootPath string
	if batch.RootFolderID != nil {
		if rootPath = f.pathOf(*batch.RootFolderID, batch.UserID); rootPath == "" {
			return FolderRecorded{}, ErrRootFolderGone
		}
		recorded.RootFolderID = *batch.RootFolderID
	} else {
		rootPath = batch.UserID + ":" + batch.FolderName
		recorded.RootFolderID = f.ensure(rootPath)
		f.watched[recorded.RootFolderID] = batch.Parser
	}
	for index, file := range batch.Files {
		folder := recorded.RootFolderID
		path := rootPath
		for _, name := range file.Folders {
			path += "/" + name
			folder = f.ensure(path)
		}
		switch recorded.Files[index].Outcome {
		case Created:
			f.documents[file.Identity] = &document{
				id: file.DocumentID, identity: file.Identity, title: file.Name, state: "pending",
				parser: batch.Parser, vision: batch.Vision, owner: batch.UserID, originals: []Staged{file.Staged},
				kind: folderFileType, folder: folder, relative: file.RelativePath,
			}
		case Replaced:
			held := f.documents[file.Identity]
			held.state, held.parser, held.vision, held.folder = "pending", batch.Parser, batch.Vision, folder
			held.originals = append(held.originals, file.Staged)
		case Retried:
			held := f.documents[file.Identity]
			held.state, held.parser, held.vision, held.folder = "pending", batch.Parser, batch.Vision, folder
		case Duplicate:
			held := f.documents[file.Identity]
			if last := held.originals[len(held.originals)-1]; last.SHA256 == file.SHA256 {
				held.touched++
				held.folder = folder
			}
		}
	}
	return recorded, nil
}

// folderReply is what the folder route answered.
type folderReply struct {
	status  int
	answer  folderAnswer
	message string
	// raw is the body of a 200, for the test of its field names.
	raw []byte
}

func (s *site) uploadFolder(parts ...part) folderReply {
	s.t.Helper()
	body, contentType := form(s.t, parts...)
	response, err := http.Post(s.server.URL+FolderPath, contentType, body)
	if err != nil {
		s.t.Fatalf("posting: %v", err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		s.t.Fatal(err)
	}
	got := folderReply{status: response.StatusCode}
	if response.StatusCode == http.StatusOK {
		if err := json.Unmarshal(raw, &got.answer); err != nil {
			s.t.Fatalf("the answer is not JSON: %v: %s", err, raw)
		}
		got.raw = raw
		return got
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Status  int    `json:"status"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Error.Status != response.StatusCode {
		s.t.Fatalf("a refusal that is not the error envelope: %s", raw)
	}
	got.message = envelope.Error.Message
	return got
}

// paths is the relative_paths field for these paths.
func paths(t testing.TB, relative ...string) part {
	t.Helper()
	encoded, err := json.Marshal(relative)
	if err != nil {
		t.Fatal(err)
	}
	return field("relative_paths", string(encoded))
}

// The fields in the order the web app sends them.
func folderFieldsFor(t testing.TB, relative ...string) []part {
	return []part{field("folder_name", "Thesis"), paths(t, relative...), field("use_vision_llm", "false")}
}

func TestAFolderBecomesDocumentsInTheFoldersTheirPathsName(t *testing.T) {
	s := newSite(t)
	got := s.uploadFolder(append(folderFieldsFor(t, "abstract.md", "chapters/one.pdf", "chapters/drafts/two.txt"),
		field("parser", "datalab"),
		file("abstract.md", "# Abstract\n"), file("one.pdf", "%PDF-1.7 chapter one"), file("two.txt", "chapter two"))...)
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.message)
	}

	// The four fields the web and desktop apps read, by name.
	var read struct {
		Message      *string `json:"message"`
		Status       *string `json:"status"`
		RootFolderID *int64  `json:"root_folder_id"`
		FileCount    *int    `json:"file_count"`
	}
	if err := json.Unmarshal(got.raw, &read); err != nil {
		t.Fatal(err)
	}
	if read.Message == nil || *read.Message != "Folder upload started for 3 file(s)" || read.Status == nil || *read.Status != "processing" ||
		read.RootFolderID == nil || read.FileCount == nil || *read.FileCount != 3 {
		t.Fatalf("the apps' fields: %s", got.raw)
	}
	if len(got.answer.DocumentIDs) != 3 || len(got.answer.UnchangedDocumentIDs) != 0 || len(got.answer.SkippedFiles) != 0 {
		t.Errorf("answer %+v", got.answer)
	}

	// The top folder is found by the folder's name and marked as kept in
	// step, with the parser its files were sent to.
	root := s.tree.byPath[callerID+":Thesis"]
	if root == 0 || root != got.answer.RootFolderID || s.tree.watched[root] != "datalab" {
		t.Fatalf("the top folder is %d (answered %d), marked %q", root, got.answer.RootFolderID, s.tree.watched[root])
	}
	// Each file is a pending folder document, named by its last path part,
	// in the folder its path names, under a folder file's identity, with
	// the parser that was chosen: any of the configured ones.
	for relative, want := range map[string]struct{ title, folder, content string }{
		"abstract.md":             {"abstract.md", callerID + ":Thesis", "# Abstract\n"},
		"chapters/one.pdf":        {"one.pdf", callerID + ":Thesis/chapters", "%PDF-1.7 chapter one"},
		"chapters/drafts/two.txt": {"two.txt", callerID + ":Thesis/chapters/drafts", "chapter two"},
	} {
		held := s.rows.documents[FolderIdentity("Thesis", relative, callerID)]
		if held == nil {
			t.Fatalf("%s has no document", relative)
		}
		if held.title != want.title || held.kind != "LOCAL_FOLDER_FILE" || held.state != "pending" || held.parser != "datalab" ||
			held.relative != relative || held.folder != s.tree.byPath[want.folder] || held.folder == 0 {
			t.Errorf("%s was recorded as %+v, want it in folder %d", relative, held, s.tree.byPath[want.folder])
		}
		// The original is kept, under the document's key.
		original := held.originals[0]
		if !strings.HasPrefix(original.Key, fmt.Sprintf("documents/%s/%d/original/", callerID, held.id)) ||
			string(s.stored()[original.Key]) != want.content || original.SHA256 != digest(want.content) {
			t.Errorf("%s is stored as %+v", relative, original)
		}
	}
	if len(s.stored()) != 3 || len(s.queue.queued) != 3 {
		t.Errorf("%d objects stored and %d jobs queued, want 3 and 3", len(s.stored()), len(s.queue.queued))
	}
}

// A folder holds whatever it holds. A file the chosen parser does not read
// and an empty file are left out and named; the rest of the folder goes in.
func TestAFolderFileThatCannotBeReadIsLeftOutAndNamed(t *testing.T) {
	s := newSite(t)
	got := s.uploadFolder(append(folderFieldsFor(t, "notes.md", "app/tool.exe", "blank.txt"),
		file("notes.md", "# Notes\n"), file("tool.exe", "MZ not a document"), file("blank.txt", ""))...)
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.message)
	}
	if got.answer.FileCount != 3 || len(got.answer.DocumentIDs) != 1 {
		t.Errorf("answer %+v", got.answer)
	}
	skipped := map[string]string{}
	for _, file := range got.answer.SkippedFiles {
		skipped[file.RelativePath] = file.Reason
		if file.Detail == "" {
			t.Errorf("%s was skipped with no sentence for the person", file.RelativePath)
		}
	}
	if want := map[string]string{"app/tool.exe": "not_read_by_parser", "blank.txt": "empty"}; !reflect.DeepEqual(skipped, want) {
		t.Errorf("skipped %v, want %v", skipped, want)
	}
	// Only the readable file is stored and queued; the empty object is not
	// left in the store, and no folder is made for a file that was left out.
	if len(s.stored()) != 1 || len(s.queue.queued) != 1 || len(s.rows.documents) != 1 {
		t.Errorf("%d stored, %d queued, %d documents; want one of each", len(s.stored()), len(s.queue.queued), len(s.rows.documents))
	}
	if _, made := s.tree.byPath[callerID+":Thesis/app"]; made {
		t.Error("a folder was made for a file that was left out")
	}
}

// Syncing a folder again: a file whose bytes are unchanged is not indexed
// again and its second copy is not kept, but it is recorded as seen; a file
// whose bytes changed is given its new original and queued.
func TestSyncingAFolderAgainIndexesOnlyWhatChanged(t *testing.T) {
	s := newSite(t)
	first := s.uploadFolder(append(folderFieldsFor(t, "kept.md", "edited.md"),
		file("kept.md", "the same"), file("edited.md", "before"))...)
	if first.status != http.StatusOK {
		t.Fatalf("status %d: %s", first.status, first.message)
	}
	kept := s.rows.documents[FolderIdentity("Thesis", "kept.md", callerID)]
	edited := s.rows.documents[FolderIdentity("Thesis", "edited.md", callerID)]
	// The indexer finished both.
	kept.state, edited.state = "ready", "ready"

	// The second request names the folder by the id the first answered, as
	// the apps do.
	second := s.uploadFolder(
		field("folder_name", "Thesis"), paths(t, "kept.md", "edited.md"),
		field("root_folder_id", strconv.FormatInt(first.answer.RootFolderID, 10)),
		file("kept.md", "the same"), file("edited.md", "after"))
	if second.status != http.StatusOK {
		t.Fatalf("status %d: %s", second.status, second.message)
	}
	if !reflect.DeepEqual(second.answer.DocumentIDs, []int64{edited.id}) || !reflect.DeepEqual(second.answer.UnchangedDocumentIDs, []int64{kept.id}) ||
		second.answer.RootFolderID != first.answer.RootFolderID {
		t.Errorf("answer %+v", second.answer)
	}
	if kept.touched != 1 || len(kept.originals) != 1 || kept.state != "ready" {
		t.Errorf("the unchanged file: %+v", kept)
	}
	if len(edited.originals) != 2 || edited.state != "pending" || edited.originals[1].SHA256 != digest("after") {
		t.Errorf("the changed file: %+v", edited)
	}
	// Three objects: both first originals and the changed file's new one.
	if len(s.stored()) != 3 || len(s.queue.queued) != 3 {
		t.Errorf("%d objects stored and %d jobs queued, want 3 and 3", len(s.stored()), len(s.queue.queued))
	}
}

func TestWhatAFolderUploadIsRefusedFor(t *testing.T) {
	deep := "a/b/c/d/e/f/g/h/i.md" // eight folders under the top one: nine levels
	for _, test := range []struct {
		name    string
		as      principal.Method
		parts   []part
		status  int
		message string
	}{
		{name: "the files before the fields",
			parts:  []part{file("a.md", "text"), field("folder_name", "Thesis"), paths(t, "a.md")},
			status: 400, message: FolderFieldsFirst},
		{name: "no relative_paths",
			parts:  []part{field("folder_name", "Thesis"), file("a.md", "text")},
			status: 400, message: FolderFieldsFirst},
		{name: "no files at all",
			parts:  folderFieldsFor(t, "a.md"),
			status: 400, message: NoFiles},
		{name: "fewer files than paths",
			parts:  append(folderFieldsFor(t, "a.md", "b.md"), file("a.md", "text")),
			status: 400, message: "Mismatch: 1 files but 2 relative_paths"},
		{name: "more files than paths",
			parts:  append(folderFieldsFor(t, "a.md"), file("a.md", "text"), file("b.md", "more")),
			status: 400, message: "Mismatch: more files than the 1 relative_paths"},
		{name: "a path that climbs out of the folder",
			parts:  append(folderFieldsFor(t, "../secrets.md"), file("secrets.md", "text")),
			status: 400, message: `"../secrets.md" cannot be used as a path inside the folder: a part of it is "." or ".."`},
		{name: "a path from the root",
			parts:  append(folderFieldsFor(t, "/etc/notes.md"), file("notes.md", "text")),
			status: 400, message: `"/etc/notes.md" cannot be used as a path inside the folder: a part of it is empty`},
		{name: "one path listed twice",
			parts:  append(folderFieldsFor(t, "a.md", "a.md"), file("a.md", "one"), file("a.md", "two")),
			status: 400, message: `"a.md" is listed twice in relative_paths`},
		{name: "a tree deeper than folders may nest",
			parts:  append(folderFieldsFor(t, deep), file("i.md", "text")),
			status: 400, message: "Folder structure too deep: 9 levels exceeds the maximum of 8."},
		{name: "relative_paths that is not a list",
			parts:  []part{field("folder_name", "Thesis"), field("relative_paths", `{"a.md": 1}`), file("a.md", "text")},
			status: 400, message: "relative_paths must be a JSON list of the files' paths inside the folder"},
		{name: "more paths than a request may carry",
			parts:  append(folderFieldsFor(t, "a.md", "b.md", "c.md", "d.md"), file("a.md", "text")),
			status: 400, message: "A folder upload carries at most 3 files; send the rest in another request"},
		{name: "a field the form does not have",
			parts:  append(folderFieldsFor(t, "a.md"), field("processing_mode", "premium"), file("a.md", "text")),
			status: 400, message: `This folder upload form has no "processing_mode" field; it takes folder_name, relative_paths, root_folder_id, use_vision_llm, parser and files`},
		{name: "a vision model neither the account nor the server has",
			parts:  []part{field("folder_name", "Thesis"), paths(t, "a.md"), field("use_vision_llm", "true"), file("a.md", "text")},
			status: 400, message: uploadvision.NoModel},
		{name: "a parser this server has no key for",
			parts:  append(folderFieldsFor(t, "a.md"), field("parser", "llamaparse"), file("a.md", "text")),
			status: parserchoice.Status, message: "The llamaparse parser is not configured on this server. Choose another parser, or ask the administrator to configure it."},
		// The files are the caller's; a form that names an owner is refused
		// like any other field the form has not got.
		{name: "a form that names whose folder this is",
			parts:  []part{field("folder_name", "Thesis"), field("workspace_id", "8"), paths(t, "a.md"), file("a.md", "text")},
			status: 400, message: `This folder upload form has no "workspace_id" field; it takes folder_name, relative_paths, root_folder_id, use_vision_llm, parser and files`},
		{name: "a root folder the account does not have",
			parts:  append(folderFieldsFor(t, "a.md"), field("root_folder_id", "4040"), file("a.md", "text")),
			status: 404, message: RootFolderAbsent},
		{name: "a file past the per-file limit",
			parts:  append(folderFieldsFor(t, "a.md", "big.md"), file("a.md", "text"), file("big.md", strings.Repeat("x", 1<<10+1))),
			status: 413, message: `"big.md" is larger than the 1.0 KB per-file limit`},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newSite(t)
			if test.as != "" {
				s.as.Method = test.as
			}
			got := s.uploadFolder(test.parts...)
			if got.status != test.status || (test.message != "" && got.message != test.message) {
				t.Fatalf("status %d %q, want %d %q", got.status, got.message, test.status, test.message)
			}
			// A refused request leaves nothing: no object, no row, no
			// folder, no job.
			if len(s.stored()) != 0 || len(s.rows.documents) != 0 || len(s.tree.byPath) != 0 || len(s.queue.queued) != 0 {
				t.Errorf("a refused folder upload left %d objects, %d documents, %d folders and %d jobs",
					len(s.stored()), len(s.rows.documents), len(s.tree.byPath), len(s.queue.queued))
			}
		})
	}
}

// The transaction failing after the files are stored: nothing is recorded
// and the stored files are removed.
func TestAFolderUploadIsRecordedWholeOrNotAtAll(t *testing.T) {
	s := newSite(t)
	s.tree.recordErr = fmt.Errorf("the database went away")
	got := s.uploadFolder(append(folderFieldsFor(t, "a.md", "sub/b.md"), file("a.md", "one"), file("b.md", "two"))...)
	if got.status != http.StatusInternalServerError {
		t.Fatalf("status %d: %s", got.status, got.message)
	}
	if len(s.stored()) != 0 || len(s.queue.queued) != 0 {
		t.Errorf("%d objects left in the store, %d jobs queued", len(s.stored()), len(s.queue.queued))
	}
}

// A folder file's identity: sha256 of
// "LOCAL_FOLDER_FILE:{folder}:{path}:{user_id}". A sync finds the file's
// document by it, so it must not move, and it must differ by account.
func TestAFolderFilesIdentityIsTheOneASyncLooksUp(t *testing.T) {
	want := digest("LOCAL_FOLDER_FILE:Thesis:chapters/one.pdf:" + callerID)
	if got := FolderIdentity("Thesis", "chapters/one.pdf", callerID); got != want {
		t.Errorf("identity %s, want %s", got, want)
	}
	if FolderIdentity("Thesis", "chapters/one.pdf", otherID) == want {
		t.Error("two accounts' files of one path have one identity")
	}
}

// Account B cannot put files under account A's folder: A's folder id
// answers exactly as an id no folder has, and nothing is stored or made.
// And the same folder synced by both is two folders and two documents.
func TestAFolderUploadCannotReachAnotherAccountsFolder(t *testing.T) {
	s := newSite(t)
	first := s.uploadFolder(append(folderFieldsFor(t, "a.md"), file("a.md", "A's text"))...)
	if first.status != http.StatusOK {
		t.Fatalf("account A's folder upload: %d %s", first.status, first.message)
	}
	before, objects := len(s.rows.documents), len(s.stored())

	s.as.UserID = otherID
	into := s.uploadFolder(
		field("folder_name", "Thesis"), paths(t, "b.md"),
		field("root_folder_id", strconv.FormatInt(first.answer.RootFolderID, 10)), file("b.md", "B's text"))
	absent := s.uploadFolder(
		field("folder_name", "Thesis"), paths(t, "b.md"), field("root_folder_id", "999999"), file("b.md", "B's text"))
	if into.status != http.StatusNotFound || into.message != RootFolderAbsent {
		t.Fatalf("B uploading into A's folder answered %d %q, want 404 %q", into.status, into.message, RootFolderAbsent)
	}
	if into.status != absent.status || into.message != absent.message {
		t.Errorf("A's folder answered %d %q and a folder never made %d %q; they must be the same answer",
			into.status, into.message, absent.status, absent.message)
	}
	if len(s.rows.documents) != before || len(s.stored()) != objects {
		t.Errorf("the refused uploads left %d documents and %d objects, were %d and %d",
			len(s.rows.documents), len(s.stored()), before, objects)
	}

	// B syncing a folder of the same name and file gets its own.
	own := s.uploadFolder(append(folderFieldsFor(t, "a.md"), file("a.md", "A's text"))...)
	if own.status != http.StatusOK || len(own.answer.DocumentIDs) != 1 || len(own.answer.UnchangedDocumentIDs) != 0 {
		t.Fatalf("B's own folder upload: %d %+v", own.status, own.answer)
	}
	if own.answer.RootFolderID == first.answer.RootFolderID || own.answer.DocumentIDs[0] == first.answer.DocumentIDs[0] {
		t.Errorf("B was given A's folder %d or document %d", first.answer.RootFolderID, first.answer.DocumentIDs[0])
	}
}

// streamedFolder is a folder upload of `count` files of `size` bytes each,
// written through a pipe as a client sends it, so that the benchmark's own
// side holds no file and what is measured is the route.
func streamedFolder(folder string, count int, size int64) (io.Reader, string) {
	reader, writer := io.Pipe()
	form := multipart.NewWriter(writer)
	go func() {
		relative := make([]string, count)
		for index := range relative {
			relative[index] = fmt.Sprintf("part%d/file%d.md", index%3, index)
		}
		encoded, err := json.Marshal(relative)
		for _, text := range [][2]string{{fieldFolderName, folder}, {fieldRelativePaths, string(encoded)}} {
			if err == nil {
				err = form.WriteField(text[0], text[1])
			}
		}
		for index := 0; index < count && err == nil; index++ {
			var target io.Writer
			if target, err = form.CreateFormFile(fieldFiles, fmt.Sprintf("file%d.md", index)); err == nil {
				_, err = io.Copy(target, &pattern{left: size})
			}
		}
		if err == nil {
			err = form.Close()
		}
		writer.CloseWithError(err)
	}()
	return reader, form.FormDataContentType()
}

// BenchmarkFolderUpload is the whole folder route for one request of ten
// files, which is the web app's batch: the form, ten streamed writes to a
// local store with their SHA-256, and the rows and queue (stand-ins). MB/s
// is the throughput; B/op is what a request allocates, which must not grow
// with the files.
//
// Measured on the development host (AMD EPYC 9645, 20 threads), 2026-11-11,
// with `go test -run xxx -bench FolderUpload -benchmem ./internal/uploads/`:
//
//	10 x 16 KB    3.2 ms/op    51 MB/s   775 KB/op   1415 allocs/op
//	10 x 2 MB      68 ms/op   307 MB/s   783 KB/op   2050 allocs/op
//
// A hundred and twenty-eight times the bytes cost 8 KB more: the allocation
// is the request's own (ten files' copy buffers and digests), not the
// files'. The Python route held every file of the request in memory at
// once and then wrote each to a temporary file.
func BenchmarkFolderUpload(b *testing.B) {
	for _, kilobytes := range []int64{16, 2048} {
		b.Run(fmt.Sprintf("10x%dKB", kilobytes), func(b *testing.B) {
			size := kilobytes << 10
			s := newSiteWith(b, parserchoice.Limits{MaxFileBytes: size, MaxFiles: 10})
			b.SetBytes(10 * size)
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				// A folder of its own each time: the same paths with the
				// same bytes would be unchanged files, a different path.
				body, contentType := streamedFolder(fmt.Sprintf("Folder %d", iteration), 10, size)
				response, err := http.Post(s.server.URL+FolderPath, contentType, body)
				if err != nil {
					b.Fatal(err)
				}
				_, _ = io.Copy(io.Discard, response.Body)
				response.Body.Close()
				if response.StatusCode != http.StatusOK {
					b.Fatalf("status %d", response.StatusCode)
				}
			}
		})
	}
}

func TestAFolderUploadWithVisionOnStoresItAndTakesPictures(t *testing.T) {
	s := newSite(t)
	s.vision.refusal = ""
	got := s.uploadFolder(field("folder_name", "Thesis"), paths(t, "scan.png", "notes.md"), field("use_vision_llm", "true"),
		file("scan.png", "\x89PNG picture"), file("notes.md", "# Notes\n"))
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.message)
	}
	for _, name := range []string{"scan.png", "notes.md"} {
		if held := s.titled(name); held == nil || !held.vision {
			t.Errorf("%s was recorded as %+v", name, held)
		}
	}
}
