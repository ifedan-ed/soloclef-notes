package indexing

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/embedding"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexstore"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/vectorindex"
)

const note = "# Dosing\n\nGive it once daily.\n\nWith food.\n"

func TestAStoredFileBecomesSearchableChunks(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)

	result := h.mustIndex()

	if got := h.database.contents(); !reflect.DeepEqual(got, []string{"# Dosing\n\n", "Give it once daily.\n\n", "With food.\n"}) {
		t.Fatalf("chunks = %q", got)
	}
	if result.Chunks != 3 || result.Embedded != 3 || result.Bytes != int64(len(note)) {
		t.Errorf("result = %+v", result)
	}
	// Each chunk row says where it is: its position and the lines its text
	// sits on, counted from 1, without the blank line that follows it.
	wantLines := [][2]int{{1, 1}, {3, 3}, {5, 5}}
	for position, id := range h.database.ids() {
		place := h.database.rows[id].place
		if place.Position != position || place.StartLine != wantLines[position][0] || place.EndLine != wantLines[position][1] {
			t.Errorf("chunk %d is placed %+v, want lines %v", position, place, wantLines[position])
		}
		if place.PageStart != nil || place.PageEnd != nil {
			t.Errorf("chunk %d of a Markdown file has a page", position)
		}
		// And each has a vector under its own id, filtered by its owner,
		// its document's type and its document.
		point, there := h.index.points[id]
		if !there {
			t.Fatalf("chunk %d (id %d) has no vector", position, id)
		}
		if point.UserID != testOwner || point.DocumentType != "FILE" || point.DocumentID != testDocument {
			t.Errorf("chunk %d's vector is filed under %+v", position, point)
		}
	}
	if len(h.index.points) != 3 {
		t.Errorf("%d vectors for 3 chunks", len(h.index.points))
	}
	if h.database.markdown != note {
		t.Errorf("the Markdown stored on the document is %q", h.database.markdown)
	}
	// The status a client watches, in order.
	want := []string{"processing/parsing/1", "processing/chunking", "processing/embedding", "processing/indexing", "ready"}
	if !reflect.DeepEqual(h.database.status, want) {
		t.Errorf("status went %v, want %v", h.database.status, want)
	}
	// Vectors are written before the rows that make them reachable.
	if got := h.journal.String(); got != "vectors rows" {
		t.Errorf("writes happened in the order %q", got)
	}
}

func TestAPagedDocumentsChunksKnowTheirPages(t *testing.T) {
	h := newHarness(t)
	h.upload("guide.pdf", "%PDF-1.7")
	// The parser's pages end in a blank line, as pymupdf4llm's do; the second
	// chunk starts on page 1 and ends on page 2.
	h.local.pages = []string{"# Guide\n\nThe dose is", " 5 mg.\n\nGive with food.\n\n", "", "Appendix.\n"}

	h.mustIndex()

	type pages struct{ from, to int }
	var got []pages
	for _, id := range h.database.ids() {
		place := h.database.rows[id].place
		if place.PageStart == nil || place.PageEnd == nil {
			t.Fatalf("a chunk of a PDF has no page: %+v", place)
		}
		got = append(got, pages{*place.PageStart, *place.PageEnd})
	}
	// Page 3 is blank and holds nothing.
	want := []pages{{1, 1}, {1, 2}, {2, 2}, {4, 4}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("pages = %v, want %v", got, want)
	}
}

func TestADocumentWithNoFileIsIndexedFromItsOwnMarkdown(t *testing.T) {
	h := newHarness(t)
	h.database.document.Type = "NOTE"
	h.database.document.SourceMarkdown = "A note.\n\nIts second paragraph.\n"

	h.mustIndex()

	if got := h.database.contents(); len(got) != 2 {
		t.Fatalf("chunks = %q", got)
	}
	for _, point := range h.index.points {
		if point.DocumentType != "NOTE" {
			t.Errorf("a note's vector is filed as %q", point.DocumentType)
		}
	}
}

// A note whose owner deleted all of its text is an empty note, not a broken
// one. Failing it would leave its old passages in search under a document
// that no longer says them, so every chunk row goes, every vector goes, and
// it is ready with nothing to find. Whitespace is no text either.
func TestANoteEmptiedOfItsTextDropsItsChunksAndIsReady(t *testing.T) {
	for _, emptied := range []string{"", " \n\n\t\n"} {
		h := newHarness(t)
		h.database.document.Type = "NOTE"
		h.database.document.SourceMarkdown = "A note.\n\nIts second paragraph.\n"
		h.mustIndex()
		if len(h.index.points) != 2 {
			t.Fatalf("the note was indexed into %d vectors", len(h.index.points))
		}
		h.database.document.SourceMarkdown = emptied
		h.journal.events = nil
		h.model.asked = nil

		result := h.mustIndex()

		if result.Chunks != 0 || result.Embedded != 0 {
			t.Errorf("emptied to %q: result = %+v", emptied, result)
		}
		if got := h.database.contents(); len(got) != 0 {
			t.Errorf("emptied to %q: chunk rows left %q", emptied, got)
		}
		if len(h.index.points) != 0 {
			t.Errorf("emptied to %q: %d vectors left in the index", emptied, len(h.index.points))
		}
		if h.database.markdown != emptied || h.database.document.State != "ready" || h.database.reason != "" {
			t.Errorf("emptied to %q: text %q, state %q, reason %q", emptied, h.database.markdown, h.database.document.State, h.database.reason)
		}
		// Nothing is embedded or written to Qdrant: the rows go, then their
		// vectors, and that is all.
		if len(h.model.asked) != 0 {
			t.Errorf("emptied to %q: the model was asked %q", emptied, h.model.asked)
		}
		if got := h.journal.String(); got != "rows unvectors" {
			t.Errorf("emptied to %q: writes happened in the order %q", emptied, got)
		}
	}
}

// A note that never had text and is indexed anyway is ready with no chunks
// as well, and writes nothing to Qdrant.
func TestANoteThatNeverHadTextIsReadyWithNoChunks(t *testing.T) {
	h := newHarness(t)
	h.database.document.Type = "NOTE"

	result := h.mustIndex()

	if result.Chunks != 0 || len(h.index.points) != 0 || h.journal.String() != "rows" {
		t.Fatalf("result = %+v, %d vectors, writes %q", result, len(h.index.points), h.journal.String())
	}
}

// A restored version of an uploaded file's text is indexed as restored: the
// file is not parsed again over it.
func TestARestoredVersionOfAFilesTextIsIndexedAndTheFileIsNotParsed(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)
	h.database.document.SourceMarkdown = "# Dosing\n\nAn older text, restored.\n"
	h.database.document.TextIsNewer = true

	h.mustIndex()

	if got := h.database.contents(); !reflect.DeepEqual(got, []string{"# Dosing\n\n", "An older text, restored.\n"}) {
		t.Fatalf("chunks = %q", got)
	}
	if h.database.markdown != "# Dosing\n\nAn older text, restored.\n" {
		t.Fatalf("the document's text became %q", h.database.markdown)
	}
}

// Redelivery after the job finished: nothing is embedded, nothing moves.
func TestIndexingTwiceEmbedsNothingTheSecondTime(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)
	h.mustIndex()
	ids := h.database.ids()

	second := h.mustIndex()

	if second.Embedded != 0 || second.Chunks != 3 {
		t.Errorf("the second run embedded %d of %d chunks", second.Embedded, second.Chunks)
	}
	if !reflect.DeepEqual(h.database.ids(), ids) {
		t.Errorf("the chunk ids changed from %v to %v", ids, h.database.ids())
	}
	if len(h.index.points) != 3 {
		t.Errorf("%d vectors after the second run", len(h.index.points))
	}
}

func TestAnEditEmbedsOnlyWhatChangedAndDropsWhatWasRemoved(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)
	h.mustIndex()
	before := h.database.ids()
	h.model.asked = nil

	// A paragraph is added at the top, one is reworded, one is unchanged and
	// "With food." is gone.
	h.upload("dosing.md", "Read this first.\n\n# Dosing\n\nGive it twice daily.\n\n")
	h.journal.events = nil
	result := h.mustIndex()

	if got := h.model.embedded(); !reflect.DeepEqual(got, []string{"Read this first.\n\n", "Give it twice daily.\n\n"}) {
		t.Errorf("embedded %q", got)
	}
	if result.Embedded != 2 || result.Chunks != 3 {
		t.Errorf("result = %+v", result)
	}
	after := h.database.ids()
	// The heading kept its row, and moved from first to second.
	if after[1] != before[0] {
		t.Errorf("the unchanged chunk's id changed from %d to %d", before[0], after[1])
	}
	if place := h.database.rows[after[1]].place; place.Position != 1 || place.StartLine != 3 {
		t.Errorf("the unchanged chunk was not moved: %+v", place)
	}
	// The two dropped chunks' vectors are gone, and every row has one.
	for _, dropped := range before[1:] {
		if _, there := h.index.points[dropped]; there {
			t.Errorf("the vector of dropped chunk %d is still stored", dropped)
		}
	}
	for _, id := range after {
		if _, there := h.index.points[id]; !there {
			t.Errorf("chunk %d has no vector", id)
		}
	}
	// New vectors, then the swap of rows, then the old vectors.
	if got := h.journal.String(); got != "vectors rows unvectors" {
		t.Errorf("writes happened in the order %q", got)
	}
}

// A row whose vector was lost is not kept: it would be unsearchable by
// meaning with nothing saying so.
func TestAnUnchangedChunkWhoseVectorIsMissingIsEmbeddedAgain(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)
	h.mustIndex()
	lost := h.database.ids()[1]
	delete(h.index.points, lost)
	h.model.asked = nil

	h.mustIndex()

	if got := h.model.embedded(); !reflect.DeepEqual(got, []string{"Give it once daily.\n\n"}) {
		t.Errorf("embedded %q", got)
	}
	for _, id := range h.database.ids() {
		if _, there := h.index.points[id]; !there {
			t.Errorf("chunk %d has no vector", id)
		}
	}
}

func TestAParserThatIsDownHandsTheJobBackAndChangesNothing(t *testing.T) {
	h := newHarness(t)
	h.upload("guide.pdf", "%PDF-1.7")
	h.local.err = unavailable(parse.Local)

	result, err := h.run()

	if !errors.Is(err, ErrRetry) || result.Outcome != Retry {
		t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
	}
	if want := []string{"processing/parsing/1", "processing/retrying"}; !reflect.DeepEqual(h.database.status, want) {
		t.Errorf("status went %v, want %v", h.database.status, want)
	}
	if len(h.database.rows) != 0 || len(h.index.points) != 0 || len(h.model.asked) != 0 {
		t.Error("something was written or embedded for a document that was not parsed")
	}

	// Delivered again with the parser back, it is indexed.
	h.local.err, h.local.pages = nil, []string{"Back.\n"}
	if result := h.mustIndex(); result.Chunks != 1 {
		t.Errorf("result = %+v", result)
	}
	if last := h.database.status[2]; last != "processing/parsing/2" {
		t.Errorf("the second delivery was recorded as %q", last)
	}
}

func TestAFailureThatDoesNotPassFailsTheDocumentAfterTheAttemptLimit(t *testing.T) {
	h := newHarness(t)
	h.upload("guide.pdf", "%PDF-1.7")

	for attempt := 1; attempt <= 3; attempt++ {
		h.local.err = unavailable(parse.Local)
		result, err := h.run()
		if attempt < 3 {
			if !errors.Is(err, ErrRetry) {
				t.Fatalf("attempt %d: outcome = %s, err = %v", attempt, result.Outcome, err)
			}
			continue
		}
		if err != nil || result.Outcome != Failed {
			t.Fatalf("attempt %d: outcome = %s, err = %v", attempt, result.Outcome, err)
		}
		want := "The local parser: the parser service did not answer. It was tried 3 times."
		if result.Reason != want || h.database.reason != want {
			t.Errorf("reason = %q, want %q", h.database.reason, want)
		}
	}
	if h.local.calls != 3 {
		t.Errorf("the parser was called %d times", h.local.calls)
	}
}

func TestAFileTheParserCannotReadFailsAtOnceWithTheParsersReason(t *testing.T) {
	h := newHarness(t)
	h.upload("scan.pdf", "%PDF-1.7")
	h.local.err = &parse.Error{Kind: parse.NoText, Parser: parse.Local, Message: "the file has no text layer; it looks like a scan"}

	result, err := h.run()

	if err != nil || result.Outcome != Failed {
		t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
	}
	if want := "The local parser: the file has no text layer; it looks like a scan."; h.database.reason != want {
		t.Errorf("reason = %q, want %q", h.database.reason, want)
	}
	if h.local.calls != 1 {
		t.Errorf("the parser was called %d times for a failure that cannot pass", h.local.calls)
	}
}

// A re-index that fails must not take a searchable document out of search.
func TestAFailedReindexLeavesTheOldChunksSearchable(t *testing.T) {
	h := newHarness(t)
	h.upload("guide.pdf", "%PDF-1.7")
	h.local.pages = []string{"One.\n\n", "Two.\n"}
	h.mustIndex()
	rows, points := len(h.database.rows), len(h.index.points)

	h.local.err = &parse.Error{Kind: parse.ParseFailed, Parser: parse.Local, Message: "the file is damaged"}
	if result, _ := h.run(); result.Outcome != Failed {
		t.Fatalf("outcome = %s", result.Outcome)
	}

	if len(h.database.rows) != rows || len(h.index.points) != points {
		t.Errorf("the failed re-index left %d rows and %d vectors of %d and %d", len(h.database.rows), len(h.index.points), rows, points)
	}
}

func TestAParserChosenButNotConfiguredIsNeverSwappedForAnother(t *testing.T) {
	h := newHarness(t)
	h.upload("guide.pdf", "%PDF-1.7")
	h.database.document.Parser = "datalab"
	h.local.pages = []string{"The local parser could have read this.\n"}

	result, err := h.run()

	if err != nil || result.Outcome != Failed {
		t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
	}
	if h.local.calls != 0 {
		t.Error("the document was sent to the local parser although Datalab was chosen")
	}
	if !strings.HasPrefix(h.database.reason, "Datalab: this parser has no key") {
		t.Errorf("reason = %q", h.database.reason)
	}
}

// Every parser reports a file it could not open as "unavailable, try again".
// A file that is not in storage will not be there next time either.
func TestAFileMissingFromStorageFailsAtOnce(t *testing.T) {
	h := newHarness(t)
	h.upload("guide.pdf", "%PDF-1.7")
	if err := h.store.Remove(context.Background(), h.database.document.File.Key); err != nil {
		t.Fatal(err)
	}

	result, err := h.run()

	if err != nil || result.Outcome != Failed {
		t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
	}
	if want := "The uploaded file is no longer in storage. Upload it again."; h.database.reason != want {
		t.Errorf("reason = %q, want %q", h.database.reason, want)
	}
}

func TestAFileRecordedInAStoreThisProcessWasNotGivenFailsAtOnce(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)
	h.database.document.File.Backend = "s3"

	result, _ := h.run()

	if result.Outcome != Failed || !strings.Contains(h.database.reason, "file storage settings") {
		t.Fatalf("outcome = %s, reason = %q", result.Outcome, h.database.reason)
	}
}

func TestAnEmbeddingOutageHandsTheJobBackAndARefusalFails(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)

	h.model.err = &embedding.Failure{Status: http.StatusServiceUnavailable, Retryable: true}
	if result, err := h.run(); !errors.Is(err, ErrRetry) || result.Outcome != Retry {
		t.Fatalf("an outage: outcome = %s, err = %v", result.Outcome, err)
	}
	if len(h.database.rows) != 0 || len(h.index.points) != 0 {
		t.Fatal("a document that was not embedded was written")
	}

	h.model.err = &embedding.Failure{Status: http.StatusUnauthorized, Detail: "invalid proxy key sk-secret"}
	result, err := h.run()
	if err != nil || result.Outcome != Failed {
		t.Fatalf("a refusal: outcome = %s, err = %v", result.Outcome, err)
	}
	if !strings.Contains(h.database.reason, "status 401") || strings.Contains(h.database.reason, "sk-secret") {
		t.Errorf("reason = %q: it should give the status and never the proxy's own words", h.database.reason)
	}
}

func TestAVectorStoreOutageWritesNoRows(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)
	h.index.upsertErr = &vectorindex.Failure{Retryable: true, Err: errors.New("connection refused")}

	result, err := h.run()

	if !errors.Is(err, ErrRetry) || result.Outcome != Retry {
		t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
	}
	if len(h.database.rows) != 0 {
		t.Error("chunk rows were committed although their vectors were not written")
	}
}

func TestVectorsWrittenForRowsThatWereNotCommittedAreRemoved(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)
	h.database.replaceErr = errors.New("the connection was reset")

	result, err := h.run()

	if !errors.Is(err, ErrRetry) || result.Outcome != Retry {
		t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
	}
	if len(h.index.points) != 0 {
		t.Errorf("%d vectors were left for rows that do not exist", len(h.index.points))
	}
}

// Two jobs for one document: the one that loses starts again from the top.
func TestAJobWhoseKeptChunksWereReplacedUnderneathItStartsAgain(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)
	h.mustIndex()
	h.database.replaceErr = indexstore.ErrChanged

	if result, err := h.run(); !errors.Is(err, ErrRetry) || result.Outcome != Retry {
		t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
	}
	if result := h.mustIndex(); result.Embedded != 0 {
		t.Errorf("the rerun embedded %d chunks of an unchanged document", result.Embedded)
	}
}

func TestOldVectorsThatCannotBeRemovedDoNotFailAReadyDocument(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)
	h.mustIndex()
	h.upload("dosing.md", "Entirely new.\n")
	h.index.deleteErr = &vectorindex.Failure{Retryable: true}

	result := h.mustIndex()

	if result.Chunks != 1 || h.database.document.State != "ready" {
		t.Errorf("result = %+v, state = %s", result, h.database.document.State)
	}
}

func TestADocumentThatIsGoneIsSkipped(t *testing.T) {
	h := newHarness(t)
	h.database.exists = false

	result, err := h.run()

	if err != nil || result.Outcome != Skipped || len(h.database.status) != 0 {
		t.Fatalf("outcome = %s, err = %v, status = %v", result.Outcome, err, h.database.status)
	}
}

func TestADocumentDeletedWhileItWasIndexedIsSkippedAndNotMarked(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)
	h.database.replaceErr = indexstore.ErrGone

	result, err := h.run()

	if err != nil || result.Outcome != Skipped {
		t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
	}
	if last := h.database.status[len(h.database.status)-1]; last == "failed" || last == "ready" {
		t.Errorf("a deleted document was marked %s", last)
	}
	if len(h.index.points) != 0 {
		t.Error("vectors were left for a deleted document")
	}
}

// A worker killed mid-document leaves it "processing". If that keeps
// happening the document is the cause, and it is not started on for ever.
func TestADocumentThatKeepsKillingItsWorkerIsFailed(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)
	h.database.document.State, h.database.document.Attempt = indexstore.StateProcessing, 3

	result, err := h.run()

	if err != nil || result.Outcome != Failed || !strings.Contains(h.database.reason, "started 3 times") {
		t.Fatalf("outcome = %s, err = %v, reason = %q", result.Outcome, err, h.database.reason)
	}
	if len(h.model.asked) != 0 {
		t.Error("the document was started on again")
	}
}

func TestAWorkerStoppingMidDocumentHandsItBackWithoutFailingIt(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", note)
	ctx, stop := context.WithCancel(context.Background())
	h.model.block = stop

	result, err := h.indexer().Index(ctx, testDocument)

	if !errors.Is(err, ErrRetry) || result.Outcome != Retry {
		t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
	}
	if last := h.database.status[len(h.database.status)-1]; last != "processing/embedding" {
		t.Errorf("the document was left %q", last)
	}
}

func TestADocumentThatCannotBeMarkedFailedIsHandedBack(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", "   \n")
	h.database.failErr = errors.New("the database is down")

	if result, err := h.run(); !errors.Is(err, ErrRetry) || result.Outcome != Retry {
		t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
	}
}

func TestAFileWithNoTextFails(t *testing.T) {
	h := newHarness(t)
	h.upload("empty.md", " \n\n")

	result, err := h.run()

	if err != nil || result.Outcome != Failed || !strings.Contains(h.database.reason, "no text") {
		t.Fatalf("outcome = %s, err = %v, reason = %q", result.Outcome, err, h.database.reason)
	}
}

func TestADatabaseThatCannotBeReadHandsTheJobBack(t *testing.T) {
	h := newHarness(t)
	h.database.loadErr = errors.New("connection refused")

	if result, err := h.run(); !errors.Is(err, ErrRetry) || result.Outcome != Retry {
		t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
	}
}

func TestReconcileUsesEachExistingRowOnceAndOnlyWithItsVector(t *testing.T) {
	existing := []indexstore.ExistingChunk{
		{ID: 1, Content: "header"}, {ID: 2, Content: "a"}, {ID: 3, Content: "header"}, {ID: 4, Content: "b"},
	}
	present := map[int64]bool{1: true, 2: true, 3: true} // 4 lost its vector
	pieces := []Piece{{Text: "header"}, {Text: "b"}, {Text: "header"}, {Text: "header"}, {Text: "c"}}

	got := reconcile(existing, present, pieces)

	if want := map[int]int64{0: 1, 2: 3}; !reflect.DeepEqual(got.kept, want) {
		t.Errorf("kept = %v, want %v", got.kept, want)
	}
	if want := []int{1, 3, 4}; !reflect.DeepEqual(got.fresh, want) {
		t.Errorf("fresh = %v, want %v", got.fresh, want)
	}
}

func TestAnIndexerWithAPartMissingIsRefused(t *testing.T) {
	h := newHarness(t)
	good := h.indexer()
	for name, config := range map[string]Config{
		"no attempts":      {MaxAttempts: 0, Vision: h.seer},
		"a negative delay": {MaxAttempts: 1, RetryDelay: -1, Vision: h.seer},
		"no vision client": {MaxAttempts: 1},
	} {
		if _, err := New(good.documents, good.files, good.parsers, good.chunker, good.embedder, good.vectors, config, good.log); err == nil {
			t.Errorf("%s: the indexer was built", name)
		}
	}
	if _, err := New(nil, good.files, good.parsers, good.chunker, good.embedder, good.vectors, h.config, good.log); err == nil {
		t.Error("an indexer with no database was built")
	}
}

func TestStatisticsCountOutcomesAndOnlyIndexedDocumentsTime(t *testing.T) {
	statistics := NewStatistics()
	statistics.Observe(Result{Outcome: Indexed, Chunks: 3, Embedded: 2, Bytes: 100, Timings: Timings{Embed: 5, Parse: 7}})
	statistics.Observe(Result{Outcome: Retry, Timings: Timings{Embed: 1000}})
	statistics.Observe(Result{Outcome: Failed})

	got := statistics.Snapshot()

	if got.Outcomes[Indexed] != 1 || got.Outcomes[Retry] != 1 || got.Outcomes[Failed] != 1 || got.Outcomes[Skipped] != 0 {
		t.Errorf("outcomes = %v", got.Outcomes)
	}
	if got.Chunks != 3 || got.Embedded != 2 || got.Bytes != 100 || got.Phases["embed"] != 5 || got.Phases["parse"] != 7 {
		t.Errorf("snapshot = %+v", got)
	}
	if len(got.Phases) != len((Timings{}).Phases()) {
		t.Errorf("phases = %v", got.Phases)
	}
}

// The phases must account for the job: a phase left out of Timings would
// make every document look faster than it was.
func TestThePhasesAddUpToNoMoreThanTheTotal(t *testing.T) {
	h := newHarness(t)
	h.upload("dosing.md", strings.Repeat("A paragraph of the guide.\n\n", 200))

	result := h.mustIndex()

	var sum int64
	for _, spent := range result.Timings.Phases() {
		if spent < 0 {
			t.Fatalf("a phase took negative time: %v", result.Timings)
		}
		sum += int64(spent)
	}
	if sum == 0 || sum > int64(result.Timings.Total) {
		t.Errorf("the phases sum to %d ns of a %d ns job", sum, result.Timings.Total)
	}
}
