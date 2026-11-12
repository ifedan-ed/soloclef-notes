package uploads

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
)

func TestAnUploadBecomesPendingDocumentsWithTheirFilesStored(t *testing.T) {
	s := newSite(t)
	var events []string
	s.rows.events, s.queue.events = &events, &events

	got := s.upload(
		field("parser", "datalab"), field("use_vision_llm", "false"),
		file("thesis.pdf", "%PDF-1.7 the thesis"), file("notes.md", "# Notes\n"),
	)
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.message)
	}

	// The answer the apps read.
	if got.answer.Message != "Files uploaded for processing" {
		t.Errorf("message %q", got.answer.Message)
	}
	if !reflect.DeepEqual(got.answer.DocumentIDs, []int64{101, 102}) {
		t.Errorf("document_ids %v, want the two new documents in the order sent", got.answer.DocumentIDs)
	}
	if got.answer.TotalFiles != 2 || got.answer.PendingFiles != 2 || got.answer.SkippedDuplicates != 0 || len(got.answer.DuplicateDocumentIDs) != 0 {
		t.Errorf("counts %+v", got.answer)
	}

	// Each document is pending, names the parser that was chosen, and owns
	// a file row that says where its bytes are.
	thesis := s.titled("thesis.pdf")
	if thesis == nil || thesis.state != "pending" || thesis.parser != "datalab" || thesis.owner != callerID {
		t.Fatalf("thesis.pdf was recorded as %+v", thesis)
	}
	original := thesis.originals[0]
	if original.Backend != "local" || original.Size != int64(len("%PDF-1.7 the thesis")) || original.SHA256 != digest("%PDF-1.7 the thesis") {
		t.Errorf("the file row says %+v", original)
	}
	if want := fmt.Sprintf("documents/%s/%d/original/", callerID, thesis.id); !strings.HasPrefix(original.Key, want) || !strings.HasSuffix(original.Key, ".pdf") {
		t.Errorf("key %q, want %s<random>.pdf", original.Key, want)
	}

	// The bytes in the store are the bytes that were sent, under that key.
	objects := s.stored()
	if len(objects) != 2 || string(objects[original.Key]) != "%PDF-1.7 the thesis" {
		t.Errorf("the store holds %d objects; thesis.pdf is %q", len(objects), objects[original.Key])
	}

	// Both jobs were queued, and only after the rows were committed: the
	// worker reads the row the moment it takes the message.
	if !reflect.DeepEqual(s.queue.queued, []int64{101, 102}) {
		t.Errorf("queued %v", s.queue.queued)
	}
	if want := []string{"recorded", "queued", "queued"}; !reflect.DeepEqual(events, want) {
		t.Errorf("order %v, want %v", events, want)
	}
}

func TestAnUploadThatNamesNoParserGetsTheDeploymentsDefault(t *testing.T) {
	s := newSite(t)
	got := s.upload(file("thesis.pdf", "bytes"))
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.message)
	}
	if parser := s.titled("thesis.pdf").parser; parser != "local" {
		t.Errorf("recorded parser %q, want the default by name, never empty", parser)
	}
}

func TestWhatAnUploadIsRefusedFor(t *testing.T) {
	big := strings.Repeat("x", int(testLimits.MaxFileBytes)+1)
	exactly := strings.Repeat("x", int(testLimits.MaxFileBytes))

	for _, test := range []struct {
		name   string
		parts  []part
		as     principal.Method
		status int
		// says is a piece of the message; allowed means a 200 instead.
		says    string
		allowed bool
	}{
		{name: "a parser with no key, by name",
			parts:  []part{field("parser", "llamaparse"), file("a.pdf", "x")},
			status: 400, says: "The llamaparse parser is not configured on this server"},
		{name: "a word that is not a parser",
			parts:  []part{field("parser", "cloud"), file("a.pdf", "x")},
			status: 400, says: `"cloud" is not a parser`},
		{name: "a file the chosen parser does not read, with what it does read",
			parts:  []part{field("parser", "local"), file("scan.png", "x")},
			status: 400, says: `"scan.png" cannot be read by the local parser: .png is not read by this parser, which reads .pdf, .docx, .epub`},
		{name: "the same file under the parser that reads it is taken",
			parts:   []part{field("parser", "datalab"), file("scan.png", "x")},
			allowed: true},
		{name: "the vision switch, with no vision model for the account or the server",
			parts:  []part{field("use_vision_llm", "true"), file("a.pdf", "x")},
			status: 400, says: "No vision model is set for this account or on this server"},
		{name: "a field sent after a file",
			parts:  []part{file("a.pdf", "x"), field("parser", "datalab")},
			status: 400, says: "Every form field must be sent before the first file"},
		{name: "a field this form does not have",
			parts:  []part{field("processing_mode", "premium"), file("a.pdf", "x")},
			status: 400, says: `no "processing_mode" field`},
		{name: "a field sent twice",
			parts:  []part{field("parser", "local"), field("parser", "datalab"), file("a.pdf", "x")},
			status: 400, says: "parser field was sent more than once"},
		// Whose documents these are is the caller, never the form: a form
		// that still names an owner is refused like any field it has not got.
		{name: "a form that names whose documents these are",
			parts:  []part{field("workspace_id", "7"), file("a.pdf", "x")},
			status: 400, says: `no "workspace_id" field`},
		{name: "a form that names an account",
			parts:  []part{field("user_id", otherID), file("a.pdf", "x")},
			status: 400, says: `no "user_id" field`},
		{name: "no files at all",
			parts:  []part{field("parser", "local")},
			status: 400, says: "No files provided"},
		{name: "an empty file",
			parts:  []part{file("a.pdf", "")},
			status: 400, says: `"a.pdf" is empty`},
		{name: "one name twice in one upload",
			parts:  []part{file("a.pdf", "one"), file("a.pdf", "two")},
			status: 400, says: `"a.pdf" was sent twice`},
		{name: "more files than one upload carries",
			parts:  []part{file("a.pdf", "1"), file("b.pdf", "2"), file("c.pdf", "3"), file("d.pdf", "4")},
			status: 400, says: "at most 3 files"},
		{name: "a file one byte over the limit",
			parts:  []part{file("small.pdf", "fits"), file("big.pdf", big)},
			status: 413, says: `"big.pdf" is larger than the 1.0 KB per-file limit`},
		{name: "a file of exactly the limit is taken",
			parts:   []part{file("edge.pdf", exactly)},
			allowed: true},
		// Whether an API key may act for the account at all is decided once,
		// in the gate in front of every route (principal.Gate), not here.
		{name: "an API key the gate let through uploads as its account",
			parts: []part{file("a.pdf", "x")}, as: principal.MethodPersonalAccessToken,
			allowed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newSite(t)
			if test.as != "" {
				s.as.Method = test.as
			}
			got := s.upload(test.parts...)
			if test.allowed {
				if got.status != http.StatusOK {
					t.Fatalf("status %d: %s", got.status, got.message)
				}
				return
			}
			if got.status != test.status || !strings.Contains(got.message, test.says) {
				t.Fatalf("answered %d %q, want %d saying %q", got.status, got.message, test.status, test.says)
			}
			// A refused upload leaves nothing: no row, no job, and no
			// object in the store, including the files that arrived before
			// the one that was refused.
			if len(s.rows.documents) != 0 || len(s.queue.queued) != 0 {
				t.Errorf("a refused upload left %d documents and %d jobs", len(s.rows.documents), len(s.queue.queued))
			}
			if objects := s.stored(); len(objects) != 0 {
				t.Errorf("a refused upload left %d objects in the store", len(objects))
			}
		})
	}
}

// The limit is part of a sentence people read, so it must be a true one at
// every size.
func TestTheLimitIsWrittenTheWayItIsRead(t *testing.T) {
	for bytes, want := range map[int64]string{50 << 20: "50 MB", 1 << 10: "1.0 KB", 3 << 19: "1.5 MB", 200: "200 bytes"} {
		if got := sizeText(bytes); got != want {
			t.Errorf("%d bytes is written %q, want %q", bytes, got, want)
		}
	}
}

func TestUploadingANameTheAccountAlreadyHas(t *testing.T) {
	for _, test := range []struct {
		name string
		// state and stored are the earlier document's.
		state, stored string
		uploaded      string
		// What must be true afterwards.
		duplicate bool
		state2    string
		originals int
		objects   int
	}{
		{name: "the same bytes, already indexed: nothing is done and the copy is not kept",
			state: "ready", stored: "one", uploaded: "one", duplicate: true, state2: "ready", originals: 1, objects: 1},
		{name: "different bytes: the document gets the new file and is indexed again",
			state: "ready", stored: "one", uploaded: "two", state2: "pending", originals: 2, objects: 2},
		{name: "the same bytes after a failure: tried again with the file it has",
			state: "failed", stored: "one", uploaded: "one", state2: "pending", originals: 1, objects: 1},
		{name: "still being indexed: left alone whatever the bytes",
			state: "processing", stored: "one", uploaded: "two", duplicate: true, state2: "processing", originals: 1, objects: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := newSite(t)
			if first := s.upload(file("report.pdf", test.stored)); first.status != http.StatusOK {
				t.Fatalf("the first upload: %d %s", first.status, first.message)
			}
			held := s.titled("report.pdf")
			held.state = test.state
			s.queue.queued = nil

			got := s.upload(field("parser", "datalab"), file("report.pdf", test.uploaded))
			if got.status != http.StatusOK {
				t.Fatalf("status %d: %s", got.status, got.message)
			}
			if test.duplicate {
				if !reflect.DeepEqual(got.answer.DuplicateDocumentIDs, []int64{held.id}) || got.answer.SkippedDuplicates != 1 || len(got.answer.DocumentIDs) != 0 {
					t.Errorf("answer %+v, want the document reported as a duplicate", got.answer)
				}
				if len(s.queue.queued) != 0 {
					t.Errorf("a duplicate queued %v", s.queue.queued)
				}
			} else {
				if !reflect.DeepEqual(got.answer.DocumentIDs, []int64{held.id}) || got.answer.PendingFiles != 1 {
					t.Errorf("answer %+v, want the same document handed back to the indexer", got.answer)
				}
				if !reflect.DeepEqual(s.queue.queued, []int64{held.id}) {
					t.Errorf("queued %v", s.queue.queued)
				}
				if held.parser != "datalab" {
					t.Errorf("the document keeps parser %q, not the one this upload chose", held.parser)
				}
			}
			if held.state != test.state2 || len(held.originals) != test.originals {
				t.Errorf("the document is %s with %d originals, want %s with %d", held.state, len(held.originals), test.state2, test.originals)
			}
			// One document, never a second of the same name; and no copy
			// is left in the store that no row points at.
			if len(s.rows.documents) != 1 {
				t.Errorf("%d documents", len(s.rows.documents))
			}
			if objects := s.stored(); len(objects) != test.objects {
				t.Errorf("%d objects in the store, want %d", len(objects), test.objects)
			}
			for _, original := range held.originals {
				if _, kept := s.stored()[original.Key]; !kept {
					t.Errorf("the file row %s names an object that is gone", original.Key)
				}
			}
		})
	}
}

func TestADocumentBeingDeletedIsNotUploadedOver(t *testing.T) {
	s := newSite(t)
	s.upload(file("report.pdf", "one"))
	s.titled("report.pdf").state = "deleting"

	got := s.upload(file("report.pdf", "two"))
	if got.status != http.StatusConflict || !strings.Contains(got.message, `"report.pdf" is being deleted`) {
		t.Fatalf("answered %d %q", got.status, got.message)
	}
	if objects := s.stored(); len(objects) != 1 {
		t.Errorf("%d objects in the store; the refused copy was kept", len(objects))
	}
}

func TestTheWholeRequestIsRecordedOrNoneOfIt(t *testing.T) {
	s := newSite(t)
	s.rows.recordErr = errors.New("the database went away")

	got := s.upload(file("a.pdf", "one"), file("b.pdf", "two"))
	if got.status != http.StatusInternalServerError {
		t.Fatalf("status %d", got.status)
	}
	if strings.Contains(got.message, "database") {
		t.Errorf("the refusal leaks the cause: %q", got.message)
	}
	if objects := s.stored(); len(objects) != 0 {
		t.Errorf("%d objects are stored for rows that were never written", len(objects))
	}
	if len(s.queue.queued) != 0 {
		t.Errorf("jobs were queued for rows that were never written: %v", s.queue.queued)
	}
}

func TestAJobThatCannotBeQueuedFailsItsDocumentNotTheUpload(t *testing.T) {
	s := newSite(t)
	s.queue.down = true

	got := s.upload(file("a.pdf", "one"))
	if got.status != http.StatusOK || !reflect.DeepEqual(got.answer.DocumentIDs, []int64{101}) {
		t.Fatalf("answered %d %+v; the document exists and must be reported", got.status, got.answer)
	}
	held := s.titled("a.pdf")
	if held.state != "failed" {
		t.Errorf("the document is %q; with no job behind it, it must not be left pending", held.state)
	}
	// The file is kept: indexing it later needs no second upload.
	if objects := s.stored(); len(objects) != 1 {
		t.Errorf("%d objects in the store", len(objects))
	}
}

func TestABodyThatIsNotAnUploadForm(t *testing.T) {
	s := newSite(t)
	for name, contentType := range map[string]string{
		"JSON":                    "application/json",
		"a form with no boundary": "multipart/form-data",
		"a URL-encoded form":      "application/x-www-form-urlencoded",
	} {
		t.Run(name, func(t *testing.T) {
			got := s.post(strings.NewReader(url.Values{"parser": {"local"}}.Encode()), contentType)
			if got.status != http.StatusBadRequest || got.message != NotMultipart {
				t.Errorf("answered %d %q", got.status, got.message)
			}
		})
	}
}

// A client that stops in the middle of a file must leave nothing behind:
// the store keeps no part of the object, and no row is written.
func TestAnUploadCutOffMidFileLeavesNothing(t *testing.T) {
	s := newSite(t)

	connection, err := net.Dial("tcp", s.server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	const boundary = "cut-here"
	head := "--" + boundary + "\r\nContent-Disposition: form-data; name=\"parser\"\r\n\r\nlocal\r\n" +
		"--" + boundary + "\r\nContent-Disposition: form-data; name=\"files\"; filename=\"half.pdf\"\r\n" +
		"Content-Type: application/pdf\r\n\r\n" + strings.Repeat("y", 300)
	// A Content-Length that promises more than is ever sent.
	request := "POST " + Path + " HTTP/1.1\r\nHost: test\r\n" +
		"Content-Type: multipart/form-data; boundary=" + boundary + "\r\n" +
		"Content-Length: 5000\r\n\r\n" + head
	if _, err := io.WriteString(connection, request); err != nil {
		t.Fatal(err)
	}
	// The file is on its way into the store while the request is still
	// open: the store's unfinished write is there before the upload ends.
	// That is the streaming, seen from outside.
	arrived := false
	for waited := time.Duration(0); waited < 5*time.Second && !arrived; waited += 20 * time.Millisecond {
		time.Sleep(20 * time.Millisecond)
		arrived = len(s.stored()) == 1
	}
	if !arrived {
		t.Fatal("nothing reached the store while the upload was in flight")
	}
	connection.Close()

	// The route learns of it on its next read, and the store discards the
	// unfinished write.
	gone := false
	for waited := time.Duration(0); waited < 5*time.Second && !gone; waited += 20 * time.Millisecond {
		time.Sleep(20 * time.Millisecond)
		gone = len(s.stored()) == 0
	}
	if !gone {
		t.Fatalf("%d objects left in the store after the client hung up", len(s.stored()))
	}
	if len(s.rows.documents) != 0 || len(s.queue.queued) != 0 {
		t.Errorf("a cut-off upload left %d documents and %d jobs", len(s.rows.documents), len(s.queue.queued))
	}
}

func TestTheDecisionForANameSeenBefore(t *testing.T) {
	for _, test := range []struct {
		name     string
		prior    *Prior
		checksum string
		want     Outcome
	}{
		{"a name the account has never seen", nil, "aa", Created},
		{"ready, same bytes", &Prior{State: "ready", Checksum: "aa"}, "aa", Duplicate},
		{"ready, different bytes", &Prior{State: "ready", Checksum: "aa"}, "bb", Replaced},
		{"ready, with no checksum on its original: cannot be called the same", &Prior{State: "ready"}, "aa", Replaced},
		{"ready with no original at all", &Prior{State: "ready", Checksum: ""}, "", Replaced},
		{"waiting for the indexer", &Prior{State: "pending", Checksum: "aa"}, "bb", Duplicate},
		{"being indexed", &Prior{State: "processing", Checksum: "aa"}, "aa", Duplicate},
		{"failed, same bytes", &Prior{State: "failed", Checksum: "aa"}, "aa", Retried},
		{"failed, different bytes", &Prior{State: "failed", Checksum: "aa"}, "bb", Replaced},
		{"being deleted", &Prior{State: "deleting", Checksum: "aa"}, "aa", BeingDeleted},
	} {
		if got := decide(test.prior, test.checksum); got != test.want {
			t.Errorf("%s: decided %d, want %d", test.name, got, test.want)
		}
	}
}

func TestTheIdentityIsTheOneEarlierUploadsWereRecordedUnder(t *testing.T) {
	// sha256 of "FILE:{name}:{user_id}". If this changes, every file
	// uploaded before is uploaded twice.
	got := Identity("report.pdf", callerID)
	if got != digest("FILE:report.pdf:"+callerID) {
		t.Errorf("identity %q is not sha256 of FILE:report.pdf:%s", got, callerID)
	}
	if Identity("report.pdf", otherID) == got || Identity("Report.pdf", callerID) == got {
		t.Error("the identity does not depend on the account, or folds the name's case")
	}
}

// Account B uploads the very file account A already has. It is B's own new
// document, stored under B's prefix, and A's is not touched: not replaced,
// not reported as B's duplicate, not handed back to the indexer.
func TestTheSameFileNameIsOneDocumentPerAccount(t *testing.T) {
	s := newSite(t)
	first := s.upload(file("report.pdf", "the same bytes"))
	if first.status != http.StatusOK || len(first.answer.DocumentIDs) != 1 {
		t.Fatalf("account A's upload answered %d %+v", first.status, first.answer)
	}
	mine := first.answer.DocumentIDs[0]

	s.as.UserID = otherID
	second := s.upload(file("report.pdf", "the same bytes"))
	if second.status != http.StatusOK || len(second.answer.DocumentIDs) != 1 || second.answer.SkippedDuplicates != 0 {
		t.Fatalf("account B's upload answered %d %+v; it must be a new document, not A's duplicate", second.status, second.answer)
	}
	theirs := second.answer.DocumentIDs[0]
	if theirs == mine {
		t.Fatalf("both accounts were given document %d", mine)
	}
	owners := map[int64]string{}
	for _, held := range s.rows.documents {
		owners[held.id] = held.owner
		if len(held.originals) != 1 {
			t.Errorf("document %d has %d originals; the other account's upload reached it", held.id, len(held.originals))
		}
		if want := fmt.Sprintf("documents/%s/%d/original/", held.owner, held.id); !strings.HasPrefix(held.originals[0].Key, want) {
			t.Errorf("document %d is stored at %q, outside its owner's prefix %s", held.id, held.originals[0].Key, want)
		}
	}
	if owners[mine] != callerID || owners[theirs] != otherID {
		t.Errorf("owners %v", owners)
	}

	// B replacing its file leaves A's exactly as it was. (B's is ready
	// first: a document still waiting for the indexer is left alone.)
	for _, held := range s.rows.documents {
		held.state = "ready"
	}
	if again := s.upload(file("report.pdf", "B's corrected bytes")); again.status != http.StatusOK || !reflect.DeepEqual(again.answer.DocumentIDs, []int64{theirs}) {
		t.Fatalf("B's second upload answered %d %+v", again.status, again.answer)
	}
	for _, held := range s.rows.documents {
		if held.id == mine && (len(held.originals) != 1 || held.originals[0].SHA256 != digest("the same bytes")) {
			t.Errorf("A's document changed when B uploaded: %+v", held)
		}
	}
}

func TestAStorageKeyCannotBeSteeredByAFileName(t *testing.T) {
	for name, wantSuffix := range map[string]string{
		"thesis.PDF":                    ".pdf",
		"archive.tar.gz":                ".gz",
		"no extension":                  "",
		"trailing.":                     "",
		"odd.p/df":                      "",
		"dots.. /.. /x.a b":             "",
		"long.aaaaaaaaaaaaaaaaaaaaaaaa": "",
	} {
		key, err := OriginalKey(callerID, 101, name)
		if err != nil {
			t.Fatal(err)
		}
		const prefix = "documents/" + callerID + "/101/original/"
		if !strings.HasPrefix(key, prefix) {
			t.Fatalf("%q: key %q", name, key)
		}
		last := strings.TrimPrefix(key, prefix)
		if strings.ContainsAny(last, "/ ") || len(last) != 32+len(wantSuffix) || !strings.HasSuffix(last, wantSuffix) {
			t.Errorf("%q: key ends %q, want 32 hex digits and %q", name, last, wantSuffix)
		}
	}
	first, _ := OriginalKey(callerID, 101, "a.pdf")
	second, _ := OriginalKey(callerID, 101, "a.pdf")
	if first == second {
		t.Error("two keys for the same file are equal; a re-upload would collide")
	}
}
