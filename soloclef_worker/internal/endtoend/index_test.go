package endtoend

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/queues"
	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/broker"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexing"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexstore"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indextask"
)

// formulary is a Markdown file with prose and a table long enough to be cut
// into several chunks at 512 tokens, so the header has to be repeated.
func formulary() string {
	var text strings.Builder
	text.WriteString("# Ward formulary\n\n")
	text.WriteString("This guide lists the antibiotics kept on the ward and how each is given.\n\n")
	text.WriteString(strings.Repeat("Check the weight and the allergies before every first dose. ", 30))
	text.WriteString("\n\n| Drug | Dose | Route |\n| --- | --- | --- |\n")
	for row := range 120 {
		fmt.Fprintf(&text, "| Antibiotic number %03d | %d mg per kilogram | oral |\n", row, 10+row)
	}
	text.WriteString("\nVancomycin levels are drawn before the fourth dose and never from the infusion line.\n")
	return text.String()
}

func TestAStoredFileBecomesParsedChunkedEmbeddedAndSearchable(t *testing.T) {
	s := open(t, false)
	content := formulary()
	documentID := s.upload(t, s.userID, "formulary.md", []byte(content), "")

	result := s.index(t, documentID)

	// The status the web app reads, and nothing left over from processing.
	if got := s.status(t, documentID); !reflect.DeepEqual(got, map[string]any{"state": "ready"}) {
		t.Errorf("status = %v", got)
	}
	chunks := s.chunks(t, documentID)
	if len(chunks) < 4 || result.Chunks != len(chunks) || result.Embedded != len(chunks) {
		t.Fatalf("%d chunk rows; result = %+v", len(chunks), result)
	}
	if result.Bytes != int64(len(content)) {
		t.Errorf("fetched %d bytes of a %d-byte file", result.Bytes, len(content))
	}

	// Every chunk has its place, and the places follow the document.
	lines := strings.Split(content, "\n")
	headers := 0
	for position, row := range chunks {
		if row.Position != position {
			t.Errorf("chunk %d is at position %d", position, row.Position)
		}
		if row.StartLine < 1 || row.EndLine < row.StartLine || row.EndLine > len(lines) {
			t.Errorf("chunk %d is on lines %d-%d of %d", position, row.StartLine, row.EndLine, len(lines))
		}
		if row.PageStart != nil || row.PageEnd != nil {
			t.Errorf("chunk %d of a Markdown file has a page", position)
		}
		if strings.HasPrefix(row.Content, "| Drug | Dose | Route |") {
			headers++
			// A table chunk's lines are its rows', not the repeated header's:
			// the first row it holds is on its first line.
			firstRow := strings.SplitN(row.Content, "\n", 4)[2]
			if position > 0 && lines[row.StartLine-1] != firstRow && !strings.HasPrefix(lines[row.StartLine-1], "| Drug") {
				t.Errorf("chunk %d says it starts on line %d (%q) and its first row is %q", position, row.StartLine, lines[row.StartLine-1], firstRow)
			}
		}
	}
	if headers < 2 {
		t.Errorf("%d chunks carry the table's header; the table should have been cut into several", headers)
	}

	// The Markdown is on the row, so a later re-index needs no parser.
	var stored, source string
	var needsReindex bool
	if err := s.pool.QueryRow(context.Background(),
		`SELECT content, source_markdown, content_needs_reindexing FROM documents WHERE id = $1`, documentID).
		Scan(&stored, &source, &needsReindex); err != nil {
		t.Fatal(err)
	}
	if stored != content || source != content || needsReindex {
		t.Errorf("the document row holds %d and %d bytes of a %d-byte file, needs reindexing = %v", len(stored), len(source), len(content), needsReindex)
	}

	// Qdrant is the only vector store, and the schema has no column for a
	// vector: one point a chunk.
	if points := s.pointCount(t, documentID); points != len(chunks) {
		t.Errorf("%d points for %d chunks", points, len(chunks))
	}

	// And it is searchable, by the request the product's reader makes.
	hits, err := s.search(s.userID, "vancomycin levels drawn before the fourth dose", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Content, "Vancomycin levels are drawn") {
		t.Fatalf("the search did not find the sentence first: %+v", hits)
	}
	want := map[string]any{
		"user_id": s.userID, "document_type": "FILE", "document_id": float64(documentID),
	}
	if !reflect.DeepEqual(hits[0].Payload, want) {
		t.Errorf("payload = %v, want %v", hits[0].Payload, want)
	}

	// Another account asking the same question gets nothing of this one's.
	other := s.newAccount(t)
	if hits, err := s.search(other, "vancomycin levels drawn before the fourth dose", 5); err != nil || len(hits) != 0 {
		t.Errorf("another account's search returned %d hits (err %v)", len(hits), err)
	}

	// The phases, for the record.
	t.Logf("indexed %d bytes into %d chunks in %s: %v", result.Bytes, result.Chunks, result.Timings.Total.Round(time.Millisecond), result.Timings.Phases())
}

func TestAPDFGoesThroughTheParserServiceAndItsChunksKnowTheirPages(t *testing.T) {
	s := open(t, false)
	document := pdf(
		[]string{"Dosing guide", "Amoxicillin is given three times a day with or without food."},
		[]string{"Storage", "Keep the reconstituted suspension in the refrigerator for seven days."},
	)
	documentID := s.upload(t, s.userID, "dosing guide.pdf", document, "local")

	result := s.index(t, documentID)

	chunks := s.chunks(t, documentID)
	if len(chunks) == 0 {
		t.Fatal("no chunks")
	}
	var text strings.Builder
	for _, row := range chunks {
		if row.PageStart == nil || row.PageEnd == nil || *row.PageStart < 1 || *row.PageEnd > 2 || *row.PageEnd < *row.PageStart {
			t.Fatalf("chunk %d has pages %v-%v", row.Position, row.PageStart, row.PageEnd)
		}
		text.WriteString(row.Content)
	}
	if !strings.Contains(text.String(), "Amoxicillin is given three times a day") || !strings.Contains(text.String(), "refrigerator for seven days") {
		t.Errorf("the chunks do not hold the PDF's text: %q", text.String())
	}
	// The first chunk starts on page 1 and the last ends on page 2.
	if *chunks[0].PageStart != 1 || *chunks[len(chunks)-1].PageEnd != 2 {
		t.Errorf("pages run %d to %d", *chunks[0].PageStart, *chunks[len(chunks)-1].PageEnd)
	}
	hits, err := s.search(s.userID, "refrigerator seven days suspension", 3)
	if err != nil || len(hits) == 0 || hits[0].DocumentID != documentID {
		t.Fatalf("the PDF is not searchable: %+v (err %v)", hits, err)
	}
	if hits[0].PageEnd == nil || *hits[0].PageEnd != 2 {
		t.Errorf("the hit about storage is cited to page %v, want 2", hits[0].PageEnd)
	}
	t.Logf("indexed a %d-byte PDF into %d chunks in %s: %v", len(document), result.Chunks, result.Timings.Total.Round(time.Millisecond), result.Timings.Phases())
}

func TestIndexingAgainIsSafeAndEmbedsNothing(t *testing.T) {
	s := open(t, false)
	documentID := s.upload(t, s.userID, "formulary.md", []byte(formulary()), "")
	s.index(t, documentID)
	before := s.chunks(t, documentID)

	// The same message, delivered again.
	again := s.index(t, documentID)

	if again.Embedded != 0 {
		t.Errorf("the second delivery embedded %d chunks", again.Embedded)
	}
	if after := s.chunks(t, documentID); !reflect.DeepEqual(after, before) {
		t.Error("the second delivery changed the chunk rows")
	}
	if points := s.pointCount(t, documentID); points != len(before) {
		t.Errorf("%d points for %d chunks after the second delivery", points, len(before))
	}
}

// A re-index of an edited file, with a reader running the whole time. The
// keyword leg reads rows, and must only ever see all of the old chunks or
// all of the new: never a mixture and never none. The meaning leg must
// never return a mixture either.
func TestAReindexReplacesTheChunksAtomicallyForAReader(t *testing.T) {
	s := open(t, false)
	old := formulary()
	documentID := s.upload(t, s.userID, "formulary.md", []byte(old), "")
	s.index(t, documentID)
	oldRows := s.chunks(t, documentID)

	// The edit: the same table under a new introduction and a new last line.
	edited := strings.Replace(old, "This guide lists the antibiotics kept on the ward", "This revised guide lists the antimicrobials stocked on the unit", 1)
	edited = strings.Replace(edited, "Vancomycin levels are drawn before the fourth dose", "Gentamicin levels are drawn before the third dose", 1)
	s.attach(t, s.userID, documentID, "formulary.md", []byte(edited))

	contents := func(rows []chunk) string {
		texts := make([]string, len(rows))
		for index, row := range rows {
			texts[index] = row.Content
		}
		return strings.Join(texts, "\x00")
	}
	oldText := contents(oldRows)

	stop := make(chan struct{})
	var (
		readers  sync.WaitGroup
		mu       sync.Mutex
		problems []string
		reads    int
	)
	report := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		if len(problems) < 5 {
			problems = append(problems, fmt.Sprintf(format, args...))
		}
	}
	var newText string
	var newTextMu sync.Mutex
	readers.Add(2)
	go func() { // the keyword leg: rows only
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			rows, err := s.pool.Query(context.Background(),
				`SELECT content FROM chunks WHERE document_id = $1 ORDER BY "position"`, documentID)
			if err != nil {
				report("reading the rows: %v", err)
				return
			}
			var texts []string
			for rows.Next() {
				var content string
				_ = rows.Scan(&content)
				texts = append(texts, content)
			}
			rows.Close()
			seen := strings.Join(texts, "\x00")
			mu.Lock()
			reads++
			mu.Unlock()
			newTextMu.Lock()
			known := newText
			newTextMu.Unlock()
			switch {
			case seen == oldText:
			case len(texts) == 0:
				report("a reader saw the document with no chunks")
			case strings.Contains(seen, "Vancomycin levels") || strings.Contains(seen, "This guide lists"):
				report("a reader saw old chunks beside new ones (%d rows)", len(texts))
			case known != "" && seen != known:
				report("a reader saw a set of rows that is neither the old nor the new")
			}
		}
	}()
	go func() { // the meaning leg: Qdrant, then the rows
		defer readers.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			hits, err := s.search(s.userID, "levels are drawn before the dose and never from the infusion line", 50)
			if err != nil {
				report("searching: %v", err)
				return
			}
			sawOld, sawNew := false, false
			for _, found := range hits {
				if found.DocumentID != documentID {
					continue
				}
				if strings.Contains(found.Content, "Vancomycin levels") || strings.Contains(found.Content, "This guide lists") {
					sawOld = true
				}
				if strings.Contains(found.Content, "Gentamicin levels") || strings.Contains(found.Content, "This revised guide") {
					sawNew = true
				}
			}
			if sawOld && sawNew {
				report("one search returned old chunks and new chunks together")
			}
		}
	}()

	result := s.index(t, documentID)
	newRows := s.chunks(t, documentID)
	newTextMu.Lock()
	newText = contents(newRows)
	newTextMu.Unlock()
	// Let the readers see the settled state too.
	time.Sleep(100 * time.Millisecond)
	close(stop)
	readers.Wait()

	for _, problem := range problems {
		t.Error(problem)
	}
	if reads < 10 {
		t.Errorf("the reader only read %d times; the test proved nothing", reads)
	}

	// Only what changed was embedded, and the table's chunks kept their rows.
	if result.Embedded == 0 || result.Embedded >= result.Chunks {
		t.Errorf("the edit embedded %d of %d chunks", result.Embedded, result.Chunks)
	}
	oldIDs := map[int64]bool{}
	for _, row := range oldRows {
		oldIDs[row.ID] = true
	}
	kept := 0
	for _, row := range newRows {
		if oldIDs[row.ID] {
			kept++
		}
	}
	if kept != result.Chunks-result.Embedded {
		t.Errorf("%d rows kept their id and %d chunks were not embedded", kept, result.Chunks-result.Embedded)
	}
	// No vector outlives its row.
	if points := s.pointCount(t, documentID); points != len(newRows) {
		t.Errorf("%d points for %d chunks after the re-index", points, len(newRows))
	}
	hits, err := s.search(s.userID, "gentamicin levels drawn before the third dose", 3)
	if err != nil || len(hits) == 0 || !strings.Contains(hits[0].Content, "Gentamicin levels") {
		t.Errorf("the new text is not searchable: %+v (err %v)", hits, err)
	}
	t.Logf("the readers read %d times during a re-index that embedded %d of %d chunks", reads, result.Embedded, result.Chunks)
}

func TestFailuresAreMarkedWithAReasonAPersonCanRead(t *testing.T) {
	s := open(t, false)
	ctx := context.Background()

	t.Run("a parser that is chosen and not configured", func(t *testing.T) {
		documentID := s.upload(t, s.userID, "guide.pdf", pdf([]string{"Text."}), "datalab")
		result, err := s.indexer.Index(ctx, documentID)
		if err != nil || result.Outcome != indexing.Failed {
			t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
		}
		status := s.status(t, documentID)
		reason, _ := status["reason"].(string)
		if status["state"] != "failed" || !strings.HasPrefix(reason, "Datalab: this parser has no key") {
			t.Errorf("status = %v", status)
		}
		if len(s.chunks(t, documentID)) != 0 {
			t.Error("a failed document has chunks: it was parsed by a parser nobody chose")
		}
	})

	t.Run("a PDF with no text layer", func(t *testing.T) {
		documentID := s.upload(t, s.userID, "scan.pdf", pdf([]string{}, []string{}), "")
		result, err := s.indexer.Index(ctx, documentID)
		if err != nil || result.Outcome != indexing.Failed {
			t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
		}
		if status := s.status(t, documentID); status["state"] != "failed" || !strings.HasPrefix(status["reason"].(string), "The local parser: ") {
			t.Errorf("status = %v", status)
		}
	})

	t.Run("a file that is no longer in storage", func(t *testing.T) {
		documentID := s.upload(t, s.userID, "gone.md", []byte("Text.\n"), "")
		var key string
		if err := s.pool.QueryRow(ctx, `SELECT storage_key FROM document_files WHERE document_id = $1`, documentID).Scan(&key); err != nil {
			t.Fatal(err)
		}
		writer, _ := s.files.Writer()
		if err := writer.Remove(ctx, key); err != nil {
			t.Fatal(err)
		}
		result, err := s.indexer.Index(ctx, documentID)
		if err != nil || result.Outcome != indexing.Failed {
			t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
		}
		if status := s.status(t, documentID); status["reason"] != "The uploaded file is no longer in storage. Upload it again." {
			t.Errorf("status = %v", status)
		}
	})

	t.Run("a parser the database does not allow is refused by the column's check", func(t *testing.T) {
		_, err := s.pool.Exec(ctx, `UPDATE documents SET parser = 'tesseract' WHERE user_id = $1::uuid`, s.userID)
		if err == nil || !strings.Contains(err.Error(), "ck_documents_parser") {
			t.Errorf("err = %v, want the migration's CHECK to refuse the name", err)
		}
	})

	t.Run("a document that does not exist", func(t *testing.T) {
		result, err := s.indexer.Index(ctx, 2_000_000_000)
		if err != nil || result.Outcome != indexing.Skipped {
			t.Fatalf("outcome = %s, err = %v", result.Outcome, err)
		}
	})
}

// The whole road: a message on the queue, the worker's own consumer, and a
// document that ends ready. The message is what the upload route will send.
func TestAMessageOnTheQueueIndexesTheDocument(t *testing.T) {
	s := open(t, false)
	ctx, stop := context.WithCancel(context.Background())
	defer stop()

	options, err := redis.ParseURL(s.redisURL)
	if err != nil {
		t.Fatal(err)
	}
	rdb := redis.NewClient(options)
	defer rdb.Close()

	documentID := s.upload(t, s.userID, "formulary.md", []byte(formulary()), "")
	message, err := celery.Encode(celery.Task{
		Name: indextask.Task, ID: randomHex(t, 16), RoutingKey: indextask.Queue, DeliveryTag: randomHex(t, 16),
		Args: []any{documentID}, IgnoreResult: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := rdb.LPush(ctx, queues.Key(indextask.Queue, 0), message).Err(); err != nil {
		t.Fatal(err)
	}

	jobs := indexing.NewStatistics()
	runner, err := indextask.New(s.indexer, jobs.Observe, nil)
	if err != nil {
		t.Fatal(err)
	}
	config := broker.DefaultConfig()
	config.Name = "index-e2e@" + randomHex(t, 3)
	config.Queues = []string{indextask.Queue}
	config.QueueConcurrency = map[string]int{indextask.Queue: 2}
	config.ShutdownGrace = 10 * time.Second
	consumer, err := broker.NewConsumer(rdb, config, runner)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- consumer.Run(ctx) }()

	deadline := time.Now().Add(30 * time.Second)
	for s.status(t, documentID)["state"] != "ready" {
		if time.Now().After(deadline) {
			t.Fatalf("the document is still %v after 30s", s.status(t, documentID))
		}
		time.Sleep(20 * time.Millisecond)
	}
	stop()
	if err := <-done; err != nil {
		t.Errorf("the consumer stopped with %v", err)
	}

	if rows := s.chunks(t, documentID); len(rows) == 0 || s.pointCount(t, documentID) != len(rows) {
		t.Errorf("%d rows and %d points", len(rows), s.pointCount(t, documentID))
	}
	if got := jobs.Snapshot().Outcomes[indexing.Indexed]; got != 1 {
		t.Errorf("%d jobs were counted as indexed", got)
	}
	// Acknowledged: nothing is left waiting or claimed.
	if waiting, _ := rdb.LLen(context.Background(), queues.Key(indextask.Queue, 0)).Result(); waiting != 0 {
		t.Errorf("%d messages are still on the queue", waiting)
	}
}

// The same road with the real model proxy: the vectors are a real model's,
// so a question in other words than the document's must find the passage.
func TestWithTheRealProxyAQuestionInOtherWordsFindsThePassage(t *testing.T) {
	s := open(t, true)
	documentID := s.upload(t, s.userID, "formulary.md", []byte(formulary()), "")

	result := s.index(t, documentID)

	hits, err := s.search(s.userID, "When should blood be taken to measure the vancomycin concentration?", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) == 0 || !strings.Contains(hits[0].Content, "Vancomycin levels are drawn") {
		t.Fatalf("the real model did not rank the passage first: %+v", hits)
	}
	t.Logf("real proxy: %d chunks embedded in %s (%v)", result.Embedded, result.Timings.Embed.Round(time.Millisecond), result.Timings.Phases())
}

// Two jobs for one document, at the level where they meet: the transaction
// that swaps the chunks. The one that planned around rows that are no longer
// there must change nothing; the one that goes second with its rows intact
// takes the document whole and reports what it removed.
func TestTwoReplacementsOfOneDocumentEndAsExactlyOneOfThem(t *testing.T) {
	s := open(t, false)
	ctx := context.Background()
	documentID := s.upload(t, s.userID, "note.md", []byte("One.\n\nTwo.\n\nThree.\n"), "")
	s.index(t, documentID)
	before := s.chunks(t, documentID)
	if len(before) == 0 {
		t.Fatal("the note has no chunks")
	}

	place := func(position int) indexstore.Place {
		return indexstore.Place{Position: position, StartLine: 1, EndLine: 1}
	}
	// Job A replaces everything with two new rows.
	idsA, err := s.documents.AllocateChunkIDs(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	removedByA, err := s.documents.Replace(ctx, indexstore.Replacement{
		DocumentID: documentID, UserID: s.userID, Markdown: "A",
		New: []indexstore.NewChunk{{ID: idsA[0], Content: "A one", Place: place(0)}, {ID: idsA[1], Content: "A two", Place: place(1)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(removedByA) != len(before) {
		t.Errorf("job A removed %v of %d old rows", removedByA, len(before))
	}

	// Job B planned, before A committed, to keep the old rows. They are gone.
	idsB, _ := s.documents.AllocateChunkIDs(ctx, 1)
	_, err = s.documents.Replace(ctx, indexstore.Replacement{
		DocumentID: documentID, UserID: s.userID, Markdown: "B",
		Kept: []indexstore.KeptChunk{{ID: before[0].ID, Place: place(0)}},
		New:  []indexstore.NewChunk{{ID: idsB[0], Content: "B new", Place: place(1)}},
	})
	if !errors.Is(err, indexstore.ErrChanged) {
		t.Fatalf("err = %v, want the job told its rows changed", err)
	}
	after := s.chunks(t, documentID)
	if len(after) != 2 || after[0].Content != "A one" || after[1].Content != "A two" {
		t.Fatalf("the losing job changed the rows: %+v", after)
	}

	// Job C keeps one of A's rows and replaces the other; it is told which
	// row it removed, so that row's vector can go too.
	idsC, _ := s.documents.AllocateChunkIDs(ctx, 1)
	removedByC, err := s.documents.Replace(ctx, indexstore.Replacement{
		DocumentID: documentID, UserID: s.userID, Markdown: "C",
		Kept: []indexstore.KeptChunk{{ID: idsA[1], Place: place(0)}},
		New:  []indexstore.NewChunk{{ID: idsC[0], Content: "C new", Place: place(1)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(removedByC, []int64{idsA[0]}) {
		t.Errorf("job C removed %v, want %v", removedByC, []int64{idsA[0]})
	}
	final := s.chunks(t, documentID)
	if len(final) != 2 || final[0].ID != idsA[1] || final[0].Content != "A two" || final[1].Content != "C new" {
		t.Errorf("rows = %+v", final)
	}

	// A document being deleted is not written to.
	if _, err := s.pool.Exec(ctx, `UPDATE documents SET status = '{"state": "deleting"}' WHERE id = $1`, documentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.documents.Replace(ctx, indexstore.Replacement{DocumentID: documentID, UserID: s.userID, Markdown: "D"}); !errors.Is(err, indexstore.ErrGone) {
		t.Errorf("replacing a document being deleted: err = %v", err)
	}
	if result, err := s.indexer.Index(ctx, documentID); err != nil || result.Outcome != indexing.Skipped {
		t.Errorf("indexing a document being deleted: outcome = %s, err = %v", result.Outcome, err)
	}
}
