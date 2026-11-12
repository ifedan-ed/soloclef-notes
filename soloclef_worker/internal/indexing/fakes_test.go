package indexing

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/delimited"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/htmlmarkdown"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/pictures"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/textfile"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"

	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/indexstore"
	"git.danvics.com/danvics/soloclef/soloclef_worker/internal/vectorindex"
)

// The fakes below stand in for Postgres, Qdrant, the proxy and the parser
// service. The file store is the real local one on a temporary directory and
// the text, CSV and HTML parsers are the real ones, because they need
// nothing that is not already here.

// journal is the order things were written in, across the fakes. The order
// is the design (vectors before rows, rows before the old vectors go), so
// the tests read it.
type journal struct {
	mu     sync.Mutex
	events []string
}

func (j *journal) add(event string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.events = append(j.events, event)
}

func (j *journal) String() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return strings.Join(j.events, " ")
}

// row is a chunk row as the fake database holds it.
type row struct {
	content string
	place   indexstore.Place
}

// database is a one-document Postgres.
type database struct {
	journal *journal

	document indexstore.Document
	exists   bool
	// status is every status the document was given, in order.
	status   []string
	reason   string
	markdown string
	rows     map[int64]row
	nextID   int64

	// Failures to inject: each is returned once by the call it names.
	loadErr, beginErr, replaceErr, failErr error
}

func (d *database) Load(context.Context, int64) (indexstore.Document, error) {
	if err := take(&d.loadErr); err != nil {
		return indexstore.Document{}, err
	}
	if !d.exists {
		return indexstore.Document{}, indexstore.ErrGone
	}
	return d.document, nil
}

func (d *database) Begin(_ context.Context, _ int64, stage string, attempt int) error {
	if err := take(&d.beginErr); err != nil {
		return err
	}
	d.document.State, d.document.Attempt = indexstore.StateProcessing, attempt
	d.status = append(d.status, fmt.Sprintf("processing/%s/%d", stage, attempt))
	return nil
}

func (d *database) Stage(_ context.Context, _ int64, stage string) error {
	if d.document.State == indexstore.StateProcessing {
		d.status = append(d.status, "processing/"+stage)
	}
	return nil
}

func (d *database) Fail(_ context.Context, _ int64, reason string) error {
	if err := take(&d.failErr); err != nil {
		return err
	}
	d.document.State, d.document.Attempt, d.reason = "failed", 0, reason
	d.status = append(d.status, "failed")
	return nil
}

func (d *database) Chunks(context.Context, int64) ([]indexstore.ExistingChunk, error) {
	chunks := make([]indexstore.ExistingChunk, 0, len(d.rows))
	for _, id := range d.ids() {
		chunks = append(chunks, indexstore.ExistingChunk{ID: id, Content: d.rows[id].content})
	}
	return chunks, nil
}

func (d *database) AllocateChunkIDs(_ context.Context, count int) ([]int64, error) {
	ids := make([]int64, count)
	for index := range ids {
		d.nextID++
		ids[index] = d.nextID
	}
	return ids, nil
}

func (d *database) Replace(_ context.Context, replacement indexstore.Replacement) ([]int64, error) {
	if err := take(&d.replaceErr); err != nil {
		return nil, err
	}
	kept := map[int64]bool{}
	for _, chunk := range replacement.Kept {
		if _, there := d.rows[chunk.ID]; !there {
			return nil, indexstore.ErrChanged
		}
		kept[chunk.ID] = true
	}
	var removed []int64
	for _, id := range d.ids() {
		if !kept[id] {
			removed = append(removed, id)
			delete(d.rows, id)
		}
	}
	for _, chunk := range replacement.Kept {
		d.rows[chunk.ID] = row{content: d.rows[chunk.ID].content, place: chunk.Place}
	}
	for _, chunk := range replacement.New {
		d.rows[chunk.ID] = row{content: chunk.Content, place: chunk.Place}
	}
	d.markdown = replacement.Markdown
	d.document.State, d.document.Attempt, d.document.SourceMarkdown = "ready", 0, replacement.Markdown
	d.status = append(d.status, "ready")
	d.journal.add("rows")
	return removed, nil
}

// ids is the document's chunk ids in document order.
func (d *database) ids() []int64 {
	ids := make([]int64, len(d.rows))
	for id, chunk := range d.rows {
		ids[chunk.place.Position] = id
	}
	return ids
}

// contents is the document's chunk texts in document order.
func (d *database) contents() []string {
	texts := make([]string, 0, len(d.rows))
	for _, id := range d.ids() {
		texts = append(texts, d.rows[id].content)
	}
	return texts
}

// take returns an injected failure once.
func take(failure *error) error {
	err := *failure
	*failure = nil
	return err
}

// index is a fake Qdrant.
type index struct {
	journal *journal
	points  map[int64]vectorindex.Point

	upsertErr, deleteErr error
}

func (x *index) Upsert(_ context.Context, points []vectorindex.Point) error {
	if err := take(&x.upsertErr); err != nil {
		return err
	}
	for _, point := range points {
		x.points[point.ChunkID] = point
	}
	if len(points) > 0 {
		x.journal.add("vectors")
	}
	return nil
}

func (x *index) Delete(_ context.Context, ids []int64) error {
	if err := take(&x.deleteErr); err != nil {
		return err
	}
	for _, id := range ids {
		delete(x.points, id)
	}
	if len(ids) > 0 {
		x.journal.add("unvectors")
	}
	return nil
}

func (x *index) Present(_ context.Context, ids []int64) (map[int64]bool, error) {
	present := map[int64]bool{}
	for _, id := range ids {
		if _, there := x.points[id]; there {
			present[id] = true
		}
	}
	return present, nil
}

// model is a fake embedding model. It records what it was asked to embed.
type model struct {
	asked [][]string
	err   error
	// block, when set, is called inside the request, for a test that stops
	// the worker mid-document.
	block func()
}

func (m *model) EmbedDocuments(ctx context.Context, texts []string) ([][]float32, error) {
	m.asked = append(m.asked, texts)
	if m.block != nil {
		m.block()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := take(&m.err); err != nil {
		return nil, err
	}
	vectors := make([][]float32, len(texts))
	for index, text := range texts {
		vectors[index] = []float32{float32(len(text)), 0, 1}
	}
	return vectors, nil
}

// embedded is every text the model was asked for, flattened.
func (m *model) embedded() []string {
	var all []string
	for _, batch := range m.asked {
		all = append(all, batch...)
	}
	return all
}

// paragraphs is a chunker that cuts at blank lines, which is enough to give
// the job several chunks with true spans. The real chunker has its own
// tests, and package chunking's check that it is wired in.
type paragraphs struct{ err error }

func (p paragraphs) Pieces(markdown string) ([]Piece, error) {
	if p.err != nil {
		return nil, p.err
	}
	var pieces []Piece
	offset := 0
	for _, part := range strings.SplitAfter(markdown, "\n\n") {
		if strings.TrimSpace(part) != "" {
			pieces = append(pieces, Piece{Text: part, Start: offset, End: offset + len(part)})
		}
		offset += len(part)
	}
	return pieces, nil
}

// seer stands in for the vision models, the proxy's and an owner's own:
// it records whom each was opened for and which model was asked about
// what, and answers a description or its failure. openErr fails the
// opening itself.
type seer struct {
	mu       sync.Mutex
	opened   []string
	asked    []string
	purposes []vision.Purpose
	err      error
	openErr  error
}

func (s *seer) Open(_ context.Context, userID, name string) (pictures.Describer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.opened = append(s.opened, userID+"="+name)
	if s.openErr != nil {
		return nil, s.openErr
	}
	return seen{s, name}, nil
}

// seen is one model the seer opened.
type seen struct {
	*seer
	model string
}

func (s seen) Describe(_ context.Context, picture vision.Picture, purpose vision.Purpose) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.asked, s.purposes = append(s.asked, s.model), append(s.purposes, purpose)
	if s.err != nil {
		return "", s.err
	}
	return fmt.Sprintf("A grey scan of %d bytes.", len(picture.Data)), nil
}

// pdfParser stands in for a document parser (the parser service or a hosted
// one). It reads the file it is given, as a real one does, and answers pages
// or the failure it was told to.
type pdfParser struct {
	id    parse.ID
	pages []string
	err   error
	calls int
}

func (p *pdfParser) ID() parse.ID { return p.id }

func (p *pdfParser) Reads(extension string) bool { return extension == ".pdf" }

func (p *pdfParser) Parse(ctx context.Context, source parse.Source) (parse.Document, error) {
	p.calls++
	body, err := source.Open(ctx)
	if err != nil {
		// What every real parser does with a file it could not open.
		return parse.Document{}, &parse.Error{Kind: parse.Unavailable, Parser: p.id, Message: "the file could not be opened", Err: err}
	}
	defer body.Close()
	if _, err := io.Copy(io.Discard, body); err != nil {
		return parse.Document{}, err
	}
	if p.err != nil {
		return parse.Document{}, p.err
	}
	var builder parse.Builder
	for number, page := range p.pages {
		builder.Page(number+1, page)
	}
	return builder.Document(p.id)
}

// harness is one document and everything around it.
type harness struct {
	t        *testing.T
	journal  *journal
	database *database
	index    *index
	model    *model
	local    *pdfParser
	store    *filestore.Local
	chunker  paragraphs
	config   Config
	seer     *seer
}

const (
	testDocument = 41
	testOwner    = "0b1f2f64-3c0e-4f0a-9d52-6a1f0c7e4b11"
)

func newHarness(t *testing.T) *harness {
	t.Helper()
	store, err := filestore.OpenLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	events := &journal{}
	h := &harness{
		seer:    &seer{},
		t:       t,
		journal: events,
		database: &database{
			journal: events,
			exists:  true,
			document: indexstore.Document{
				ID: testDocument, UserID: testOwner, Type: "FILE", Title: "Guide", State: "pending",
			},
			rows:   map[int64]row{},
			nextID: 1000,
		},
		index: &index{journal: events, points: map[int64]vectorindex.Point{}},
		model: &model{},
		local: &pdfParser{id: parse.Local},
		store: store,
	}
	h.config = Config{MaxAttempts: 3, RetryDelay: 0, Vision: h.seer}
	return h
}

// upload stores a file and points the document at it, as the upload route
// will.
func (h *harness) upload(name, content string) {
	h.t.Helper()
	key := fmt.Sprintf("documents/%s/%d/original/%d-%s", testOwner, testDocument, time.Now().UnixNano(), name)
	if err := h.store.Put(context.Background(), key, []byte(content), ""); err != nil {
		h.t.Fatal(err)
	}
	h.database.document.File = &indexstore.File{Backend: filestore.LocalBackend, Key: key, Name: name, Size: int64(len(content))}
}

func (h *harness) indexer() *Indexer {
	h.t.Helper()
	text, err := textfile.New(1 << 20)
	if err != nil {
		h.t.Fatal(err)
	}
	tables, err := delimited.New(1 << 20)
	if err != nil {
		h.t.Fatal(err)
	}
	pages, err := htmlmarkdown.New(1 << 20)
	if err != nil {
		h.t.Fatal(err)
	}
	initial, err := selection.Of("local")
	if err != nil {
		h.t.Fatal(err)
	}
	// Local is configured; the three hosted parsers are listed and have no
	// key, which is how a deployment that uses only the local one looks.
	registry, err := selection.NewRegistry(
		[]parse.Parser{text, tables, pages},
		[]selection.Choice{
			{ID: parse.Local, Extensions: []string{".pdf"}, Parser: h.local},
			{ID: parse.Datalab, Extensions: []string{".pdf"}},
			{ID: parse.LlamaParse, Extensions: []string{".pdf"}},
			{ID: parse.Unstructured, Extensions: []string{".pdf"}},
		},
		initial,
	)
	if err != nil {
		h.t.Fatal(err)
	}
	stores, err := filestore.NewStores(h.store)
	if err != nil {
		h.t.Fatal(err)
	}
	indexer, err := New(h.database, stores, registry, h.chunker, h.model, h.index, h.config,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		h.t.Fatal(err)
	}
	return indexer
}

// run indexes the document once.
func (h *harness) run() (Result, error) {
	h.t.Helper()
	return h.indexer().Index(context.Background(), testDocument)
}

// mustIndex indexes the document and requires that it ended ready.
func (h *harness) mustIndex() Result {
	h.t.Helper()
	result, err := h.run()
	if err != nil || result.Outcome != Indexed {
		h.t.Fatalf("outcome = %s (%s), err = %v", result.Outcome, result.Reason, err)
	}
	return result
}

// unavailable is a parser failure that may pass.
func unavailable(parser parse.ID) error {
	return &parse.Error{Kind: parse.Unavailable, Parser: parser, Message: "the parser service did not answer", Err: errors.New("connection refused")}
}
