// Package documents is the chat's search of an account's own documents: the
// tool the model calls to ground an answer in what the user has stored.
//
// A search is two rankings of the account's chunks fused into one. The
// meaning leg embeds the query and asks the vector store (Qdrant) for the
// nearest chunks; the keyword leg asks Postgres for chunks containing the
// query's words. Reciprocal rank fusion merges them, the best chunks are
// grouped under their documents, and each passage shown to the model is
// given a number in the thread's citation registry.
//
// # What this does better than the search it replaces
//
//   - The model calls it directly. It used to hand the question to a
//     specialist that searched and wrote a summary, and then answer from
//     the summary: two extra model calls, and the writer never saw the
//     passages it was citing.
//   - The two legs run at the same time. They ran one after the other.
//   - Every chunk id the vector store returns is read back under the
//     account filter before any of its text is used, so a vector store
//     that answered about another account (a bad filter, a rebuilt
//     collection) can show nothing. Search fails; it does not leak.
//   - A vector store that is down is an error the model is told about in a
//     sentence. There is no setting that quietly searches by keyword alone
//     and presents the result as a full search.
//   - One number per passage for the whole thread, from the registry every
//     agent of the run shares: a label is never renumbered.
//   - A notebook chat's limit (Scope.Limited) is applied at the query, to
//     both legs and again when the chunks are read back, and a mention
//     outside the notebook is said to be outside it. It used to be answered
//     from the whole notebook as if those were the documents mentioned.
package documents

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// rrfK is the constant of reciprocal rank fusion: a chunk at rank r in one
// leg scores 1/(rrfK+r). Sixty is the value from the paper that introduced
// the method and the one the indexed corpus was tuned with.
const rrfK = 60

// Scope confines a search. The zero value searches everything the account
// has.
type Scope struct {
	// DocumentIDs, when not empty, are the only documents searched: the ones
	// the person mentioned with @ in this message.
	DocumentIDs []int64
	// FolderIDs are mentioned folders; each means the documents directly in
	// it.
	FolderIDs []int64

	// Limited says the search has a hard limit, Limit, that no mention can
	// widen: a notebook chat searches its notebook's enabled sources and
	// nothing else in the account. It is a field of its own, and not "Limit
	// is not nil", because the limit of a notebook with no enabled sources is
	// the empty list, and an empty list that read as "no limit" would turn
	// the most confined chat there is into a search of everything.
	Limited bool
	// Limit is the documents a limited search may read.
	Limit []int64
}

func (s Scope) mentions() bool { return len(s.DocumentIDs) > 0 || len(s.FolderIDs) > 0 }

func (s Scope) confined() bool { return s.Limited || s.mentions() }

// Outcome says why a search of a confined scope had nothing to search, so
// the tool can tell the model which of three different things happened
// instead of "no matches" for all of them.
type Outcome uint8

const (
	// Searched means the search ran; it may still have found nothing.
	Searched Outcome = iota
	// NoSources means the scope's limit is empty: a notebook with no
	// enabled sources.
	NoSources
	// OutsideLimit means everything the person mentioned is outside the
	// limit: they pointed at documents that are not sources of the notebook.
	OutsideLimit
)

// within returns the ids of list that are also in limit, in list's order.
func within(list, limit []int64) []int64 {
	allowed := make(map[int64]struct{}, len(limit))
	for _, id := range limit {
		allowed[id] = struct{}{}
	}
	out := make([]int64, 0, min(len(list), len(limit)))
	for _, id := range list {
		if _, ok := allowed[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// Embedder turns a query into the vector the chunks were indexed with.
type Embedder interface {
	Embed(ctx context.Context, text string) ([]float32, error)
}

// Neighbor is one chunk the vector store found, nearest first.
type Neighbor struct {
	ChunkID int64
	Score   float64
}

// VectorIndex is the vector store. chunkIDs, when not nil, confines the
// search to those points (a search scoped to documents); an empty non-nil
// list therefore finds nothing.
type VectorIndex interface {
	Nearest(ctx context.Context, userID string, vector []float32, chunkIDs []int64, limit int) ([]Neighbor, error)
}

// Ranked is one chunk of the keyword leg with its rank, 1 the best; ties
// share a rank.
type Ranked struct {
	ChunkID int64
	Rank    int64
}

// Chunk is one passage with the document it belongs to.
type Chunk struct {
	ID           int64
	Content      string
	Position     int64
	Page         *int64
	DocumentID   int64
	Title        string
	DocumentType string
	// Metadata is the document's metadata column, JSON or empty.
	Metadata []byte
	// Similarity is the vector store's cosine similarity between the query
	// and this passage, 0 when only the keyword leg found it. The fused
	// rank says which passages are best; this says whether a passage found
	// by meaning alone is close at all, which a rank cannot: the vector leg
	// has nearest neighbours for any query, gibberish included.
	Similarity float64
}

// Rows is the Postgres half. Every method is confined to the account.
type Rows interface {
	KeywordRanks(ctx context.Context, userID, query string, documentIDs []int64, limit int) ([]Ranked, error)
	// Chunks reads chunks back by id, dropping any that are not this
	// account's, are outside documentIDs (when given), or belong to a
	// document being deleted.
	Chunks(ctx context.Context, userID string, chunkIDs, documentIDs []int64) ([]Chunk, error)
	ChunkIDsOfDocuments(ctx context.Context, userID string, documentIDs []int64) ([]int64, error)
	DocumentsInFolders(ctx context.Context, userID string, folderIDs []int64) ([]int64, error)
}

// Limits are the sizes of a search. All four are required: they trade recall
// against how much of the model's context one search may fill, and that
// trade belongs to the deployment.
type Limits struct {
	// Candidates is how many chunks each leg contributes to the fusion.
	Candidates int
	// MaxDocuments is the most documents one search may return, whatever the
	// model asks for.
	MaxDocuments int
	// ContextChars and DocumentChars bound the text returned: per search,
	// and per document so one long document cannot take the whole budget.
	ContextChars  int
	DocumentChars int
}

func (l Limits) check() error {
	switch {
	case l.Candidates < 1:
		return fmt.Errorf("documents: Limits.Candidates must be at least 1, got %d", l.Candidates)
	case l.MaxDocuments < 1:
		return fmt.Errorf("documents: Limits.MaxDocuments must be at least 1, got %d", l.MaxDocuments)
	case l.ContextChars < 1 || l.DocumentChars < 1:
		return fmt.Errorf("documents: Limits.ContextChars and DocumentChars must be positive, got %d and %d", l.ContextChars, l.DocumentChars)
	}
	return nil
}

// Searcher runs searches. It holds no per-user state.
type Searcher struct {
	embedder Embedder
	index    VectorIndex
	rows     Rows
	limits   Limits
}

// NewSearcher checks its parts and returns a searcher.
func NewSearcher(embedder Embedder, index VectorIndex, rows Rows, limits Limits) (*Searcher, error) {
	switch {
	case embedder == nil:
		return nil, errors.New("documents: the embedder is not set")
	case index == nil:
		return nil, errors.New("documents: the vector index is not set")
	case rows == nil:
		return nil, errors.New("documents: the rows are not set")
	}
	if err := limits.check(); err != nil {
		return nil, err
	}
	return &Searcher{embedder: embedder, index: index, rows: rows, limits: limits}, nil
}

// Hit is one document of a result with the passages that matched, in the
// order they appear in the document.
type Hit struct {
	DocumentID   int64
	Title        string
	DocumentType string
	Metadata     []byte
	Chunks       []Chunk
}

// Search returns up to topK documents for query, best first.
func (s *Searcher) Search(ctx context.Context, userID, query string, scope Scope, topK int) ([]Hit, error) {
	hits, _, err := s.SearchScoped(ctx, userID, query, scope, topK)
	return hits, err
}

// SearchScoped is Search that also says why a confined search had nothing
// to search.
func (s *Searcher) SearchScoped(ctx context.Context, userID, query string, scope Scope, topK int) ([]Hit, Outcome, error) {
	topK = min(max(topK, 1), s.limits.MaxDocuments)

	// The scope first: both legs need it, and a scope that names no
	// document at all is answered without asking either.
	var documentIDs, pointIDs []int64
	if scope.confined() {
		if scope.Limited && len(scope.Limit) == 0 {
			return nil, NoSources, nil
		}
		documentIDs = append(documentIDs, scope.DocumentIDs...)
		if len(scope.FolderIDs) > 0 {
			inFolders, err := s.rows.DocumentsInFolders(ctx, userID, scope.FolderIDs)
			if err != nil {
				return nil, Searched, fmt.Errorf("documents: reading the mentioned folders: %w", err)
			}
			documentIDs = append(documentIDs, inFolders...)
		}
		if scope.Limited {
			if scope.mentions() {
				// A mention narrows the limit and can never widen it. One
				// that names nothing inside the limit finds nothing, and is
				// said to: the search this replaces fell back to the whole
				// notebook, answering about documents the person did not
				// point at as if it had searched the ones they did.
				if documentIDs = within(documentIDs, scope.Limit); len(documentIDs) == 0 {
					return nil, OutsideLimit, nil
				}
			} else {
				documentIDs = append(documentIDs, scope.Limit...)
			}
		}
		if len(documentIDs) == 0 {
			return nil, Searched, nil
		}
		var err error
		if pointIDs, err = s.rows.ChunkIDsOfDocuments(ctx, userID, documentIDs); err != nil {
			return nil, Searched, fmt.Errorf("documents: reading the scope's chunks: %w", err)
		}
		if len(pointIDs) == 0 {
			return nil, Searched, nil
		}
	}

	// The legs are independent and each is a network round trip, so they
	// run side by side. The first failure cancels the other.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg        sync.WaitGroup
		neighbors []Neighbor
		keywords  []Ranked
		vectorErr error
		wordErr   error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		vector, err := s.embedder.Embed(ctx, query)
		if err != nil {
			vectorErr = fmt.Errorf("documents: embedding the query: %w", err)
			cancel()
			return
		}
		if neighbors, err = s.index.Nearest(ctx, userID, vector, pointIDs, s.limits.Candidates); err != nil {
			vectorErr = fmt.Errorf("documents: the vector store: %w", err)
			cancel()
		}
	}()
	go func() {
		defer wg.Done()
		var err error
		if keywords, err = s.rows.KeywordRanks(ctx, userID, query, documentIDs, s.limits.Candidates); err != nil {
			wordErr = fmt.Errorf("documents: the keyword search: %w", err)
			cancel()
		}
	}()
	wg.Wait()
	// Reported in a fixed order so the same failure reads the same way; a
	// leg that only failed because the other cancelled it is not the cause.
	for _, err := range []error{vectorErr, wordErr} {
		if err != nil && !errors.Is(err, context.Canceled) {
			return nil, Searched, err
		}
	}
	if err := errors.Join(vectorErr, wordErr); err != nil {
		return nil, Searched, err
	}

	fused := fuse(neighbors, keywords, s.limits.Candidates)
	if len(fused) == 0 {
		return nil, Searched, nil
	}
	ids := make([]int64, len(fused))
	for i, f := range fused {
		ids[i] = f.chunkID
	}
	chunks, err := s.rows.Chunks(ctx, userID, ids, documentIDs)
	if err != nil {
		return nil, Searched, fmt.Errorf("documents: reading the matched chunks: %w", err)
	}
	return group(fused, chunks, topK, s.limits), Searched, nil
}

// scored is a chunk with its fused score and the best rank either leg gave
// it, which breaks ties.
type scored struct {
	chunkID  int64
	score    float64
	bestRank int64
	// similarity is the vector leg's score for the chunk, 0 when that leg
	// did not find it (Chunk.Similarity).
	similarity float64
}

// fuse merges the two legs by reciprocal rank fusion and returns the best
// window chunks, best first. Ties in score go to the better single rank and
// then the lower id, so the same inputs always give the same order.
func fuse(neighbors []Neighbor, keywords []Ranked, window int) []scored {
	byID := make(map[int64]*scored, len(neighbors)+len(keywords))
	add := func(id, rank int64) {
		s := byID[id]
		if s == nil {
			s = &scored{chunkID: id, bestRank: rank}
			byID[id] = s
		}
		s.score += 1 / float64(rrfK+rank)
		s.bestRank = min(s.bestRank, rank)
	}
	// The vector store answers scores, not ranks. Equal scores share a rank
	// and the next rank skips, as SQL's rank() does for the keyword leg, so
	// that neither leg is favoured by how it happens to number ties.
	var rank int64
	for i, n := range neighbors {
		if i == 0 || n.Score != neighbors[i-1].Score {
			rank = int64(i) + 1
		}
		add(n.ChunkID, rank)
		byID[n.ChunkID].similarity = max(byID[n.ChunkID].similarity, n.Score)
	}
	for _, k := range keywords {
		add(k.ChunkID, k.Rank)
	}
	out := make([]scored, 0, len(byID))
	for _, s := range byID {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if a.bestRank != b.bestRank {
			return a.bestRank < b.bestRank
		}
		return a.chunkID < b.chunkID
	})
	if len(out) > window {
		out = out[:window]
	}
	return out
}

// group puts the fused chunks under their documents, keeps the first topK
// documents in fused order, and admits chunks best first until the budgets
// are spent. A chunk that does not fit is skipped, not a stop: a shorter one
// further down may still fit.
func group(fused []scored, chunks []Chunk, topK int, limits Limits) []Hit {
	byID := make(map[int64]*Chunk, len(chunks))
	for i := range chunks {
		byID[chunks[i].ID] = &chunks[i]
	}
	var hits []*Hit
	index := map[int64]*Hit{}
	spent := map[int64]int{}
	total := 0
	for _, f := range fused {
		c := byID[f.chunkID]
		if c == nil {
			// Not this account's, out of scope or being deleted: the
			// re-read dropped it.
			continue
		}
		hit := index[c.DocumentID]
		if hit == nil {
			if len(hits) == topK {
				continue
			}
			hit = &Hit{DocumentID: c.DocumentID, Title: c.Title, DocumentType: c.DocumentType, Metadata: c.Metadata}
			index[c.DocumentID] = hit
			hits = append(hits, hit)
		}
		size := len([]rune(strings.TrimSpace(c.Content)))
		if total+size > limits.ContextChars || spent[c.DocumentID]+size > limits.DocumentChars {
			continue
		}
		total += size
		spent[c.DocumentID] += size
		kept := *c
		kept.Similarity = f.similarity
		hit.Chunks = append(hit.Chunks, kept)
	}
	out := make([]Hit, 0, len(hits))
	for _, hit := range hits {
		if len(hit.Chunks) == 0 {
			continue
		}
		// Reading order within a document: a passage and the one after it
		// make more sense in the order the author wrote them.
		sort.Slice(hit.Chunks, func(i, j int) bool {
			a, b := hit.Chunks[i], hit.Chunks[j]
			if a.Position != b.Position {
				return a.Position < b.Position
			}
			return a.ID < b.ID
		})
		out = append(out, *hit)
	}
	return out
}
