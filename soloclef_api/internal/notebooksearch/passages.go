package notebooksearch

import (
	"math"
	"sort"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/documents"
)

// The three ways a document is in a notebook (queries.sql, `reach`).
const (
	reachSource = 1
	reachNote   = 2
	reachFolder = 3
)

// link is how a document reaches one notebook.
type link struct {
	notebookID int64
	// sourceID is the notebook's source row, 0 for a note.
	sourceID   int64
	folderName *string
	reach      int32
}

// candidate is one passage of the search's result, in rank order.
type candidate struct {
	documentID   int64
	title        string
	documentType string
	// plain is the passage as readable text; the snippet is cut from it.
	plain string
}

// A passage that holds none of the query's words is found by meaning alone,
// and two rules decide whether it is shown.
//
// The vector leg always has nearest neighbours, however far away they are,
// so a search box that showed every passage the search returned would list
// eight notebooks for any query at all, and could never say "nothing
// matches". So such a passage must first be close: its cosine similarity
// to the query at least minSimilarity. That is the line the Python search
// box drew, for the same embedding profile (bge-m3, on which unrelated
// text scores well under it and a paraphrase well over). A deployment that
// changes the profile re-indexes everything and should look at this line
// again with it.
//
// And it must come from the three best documents of the search, where a
// paraphrase of the query belongs if it is anywhere: a close passage of
// the tenth document is still a weak answer beside nine better ones.
const (
	minSimilarity     = 0.55
	semanticDocuments = 3
)

// ranked turns the search's documents into the passages worth showing, best
// first: document by document in the search's order, and inside a document
// the passages holding the most of the query's words first, then reading
// order.
func ranked(hits []documents.Hit, terms [][]rune) []candidate {
	var out []candidate
	for place, hit := range hits {
		type passage struct {
			plain    string
			distinct int
		}
		passages := make([]passage, 0, len(hit.Chunks))
		for _, chunk := range hit.Chunks {
			plain := plainText(chunk.Content)
			if plain == "" {
				// A passage that is only markup shows nothing to read; the
				// title stands for it.
				plain = plainText(hit.Title)
			}
			runes := []rune(plain)
			distinct := distinctTerms(runes, matches(runes, terms))
			if distinct == 0 && (place >= semanticDocuments || chunk.Similarity < minSimilarity) {
				continue
			}
			passages = append(passages, passage{plain, distinct})
		}
		// Stable: equal counts keep the document's reading order.
		sort.SliceStable(passages, func(i, j int) bool { return passages[i].distinct > passages[j].distinct })
		for _, p := range passages {
			out = append(out, candidate{documentID: hit.DocumentID, title: hit.Title, documentType: hit.DocumentType, plain: p.plain})
		}
	}
	return out
}

// placed is a passage under one notebook.
type placed struct {
	candidate candidate
	link      link
	// rank is the passage's place in the whole ranking, 0 the best.
	rank int
}

// group is one notebook's passages.
type group struct {
	notebookID int64
	passages   []placed
}

// groupPassages folds the ranked passages into per-notebook groups, in
// order of each notebook's best passage, within the three caps. A document
// that several notebooks read appears under each of them.
//
// Two passes keep a notebook's few slots varied: the first takes one
// passage per document, the second fills any slots left with further
// passages of the same documents, still in rank order.
func groupPassages(ranking []candidate, links map[int64][]link, notebooks, perNotebook, passages int) []*group {
	var groups []*group
	byNotebook := map[int64]*group{}
	type item struct{ notebookID, documentID int64 }
	type slot struct {
		item
		rank int
	}
	taken := map[slot]bool{}
	used := map[item]bool{}
	total := 0
	for _, distinctOnly := range []bool{true, false} {
		for rank, c := range ranking {
			for _, l := range links[c.documentID] {
				if total >= passages {
					break
				}
				key := item{l.notebookID, c.documentID}
				if taken[slot{key, rank}] || distinctOnly && used[key] {
					continue
				}
				g := byNotebook[l.notebookID]
				if g == nil {
					if len(groups) >= notebooks {
						continue
					}
					g = &group{notebookID: l.notebookID}
					byNotebook[l.notebookID] = g
					groups = append(groups, g)
				}
				if len(g.passages) >= perNotebook {
					continue
				}
				g.passages = append(g.passages, placed{c, l, rank})
				taken[slot{key, rank}] = true
				used[key] = true
				total++
			}
		}
	}
	// The second pass appends; put each notebook's passages back in rank
	// order.
	for _, g := range groups {
		sort.SliceStable(g.passages, func(i, j int) bool { return g.passages[i].rank < g.passages[j].rank })
	}
	return groups
}

// passage is one passage as the clients read it.
type passage struct {
	Kind         string   `json:"kind"`
	Title        string   `json:"title"`
	Snippet      string   `json:"snippet"`
	Highlights   [][2]int `json:"highlights"`
	DocumentID   *int64   `json:"document_id"`
	NoteID       *int64   `json:"note_id"`
	SourceID     *int64   `json:"source_id"`
	FolderName   *string  `json:"folder_name"`
	DocumentType string   `json:"document_type"`
	Score        float64  `json:"score"`
}

// rendered is a placed passage in the clients' shape. A note opens as a
// note (note_id is its document's id, which is what a note's id is); a
// source opens its guide, by source_id.
func rendered(p placed, terms [][]rune) passage {
	shown, highlights := snippet(p.candidate.plain, terms)
	out := passage{
		Kind: "source", Title: p.candidate.title, Snippet: shown, Highlights: highlights,
		DocumentType: p.candidate.documentType,
		// The search fuses by reciprocal rank and answers an order, not a
		// number per passage, so the score is the passage's place in that
		// order in the same form: 1/(60+rank). The clients use it only to
		// tell two passages apart.
		Score: math.Round(1/float64(61+p.rank)*10000) / 10000,
	}
	documentID := p.candidate.documentID
	if p.link.reach == reachNote {
		out.Kind, out.NoteID = "note", &documentID
		return out
	}
	sourceID := p.link.sourceID
	out.DocumentID, out.SourceID, out.FolderName = &documentID, &sourceID, p.link.folderName
	return out
}
