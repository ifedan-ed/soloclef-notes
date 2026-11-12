package documents

import (
	"context"
	"log/slog"
	"math/big"
	"net/http"
	"unicode/utf8"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// TitlesPath is the mention picker's search.
const TitlesPath = "/api/v1/documents/search/titles"

// search_document_titles' two defaults. Neither parameter has a bound: the
// signature is `page: int = 0, page_size: int = 20`, with no Query(ge=…).
const (
	defaultTitlesPage     = 0
	defaultTitlesPageSize = 20
)

// shortestSearch is the `len(title.strip()) < 2` the route chooses its
// statement by: under two characters there is nothing to compare trigrams
// of, and the picker is shown the most recently updated documents instead.
const shortestSearch = 2

// TitleRow is one document as the title search reads it.
type TitleRow struct {
	ID           int64
	Title        string
	DocumentType string
}

// Titles reads what the title search reads. limit and offset are the
// statement's own, which may be negative: Postgres refuses those, and the
// refusal is the route's 500.
type Titles interface {
	// RecentTitles is the account's documents, most recently updated
	// first.
	RecentTitles(ctx context.Context, userID string, limit, offset int64) ([]TitleRow, error)
	// MatchingTitles is the account's documents whose title is like the
	// term or contains it, most alike first.
	MatchingTitles(ctx context.Context, userID, term string, limit, offset int64) ([]TitleRow, error)
}

// TitlesRoute serves GET /documents/search/titles (search_document_titles):
// the id, title and type of the documents a mention picker should offer.
//
// # Two statements
//
// A title of fewer than two characters, once stripped, lists the documents
// most recently updated. Anything longer is searched for: a title matches
// when its trigram similarity to the term is above 0.3 or when it contains
// the term (ILIKE, with the term's own `%` and `_` left as wildcards -- the
// route does not escape them), and the most alike come first. The pattern
// is built here, as the route builds it: `f"%{search_term}%"`.
//
// # The order, which is why this route could not be ported before
//
// `updated_at` is neither unique nor NOT NULL, and two documents with one
// title have one similarity. Both statements are paged, so rows that tie
// could be shown twice or not at all. Since 2026-11-11 the Python statements
// end on the id -- `updated_at DESC NULLS LAST, id DESC` and
// `similarity DESC, title, id` -- and these do too.
//
// # The paging, which has no bounds
//
// OFFSET is `page * page_size` and LIMIT is `page_size + 1`: one row more
// than the page, so that `has_more` needs no count. Nothing checks either
// number, and Python's integers have no width, so:
//
//   - a negative OFFSET or LIMIT is Postgres' refusal, and the route's 500;
//   - `page_size=-1` is LIMIT 0, which is no rows, of which there are more
//     than minus one: `{"items":[],"has_more":true}`;
//   - a product past int64 is the driver's refusal, at the statement, so it
//     comes after every other refusal.
//
// The whole body is inside `except Exception`, so every 500 carries the CORS
// headers (failure.go).
type TitlesRoute struct {
	Titles Titles
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r TitlesRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "search-document-titles",
		Method:      http.MethodGet,
		Path:        TitlesPath,
		Summary:     "The documents a mention picker should offer",
		Description: "The same response as the Python tier's " +
			"GET /api/v1/documents/search/titles, byte for byte.",
		Tags: []string{"documents"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r TitlesRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, what: "the title search could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.uncaught(noCaller, nil)
		return
	}

	requested := ctx.URL()
	query := readStarletteQuery(requested.RawQuery)

	// title, page, page_size. `title` is a string and cannot fail.
	invalid := make([]errorenvelope.Failure, 0, 2)
	title, _ := query.value("title")
	page := readUnbounded(query, "page", defaultTitlesPage, &invalid)
	pageSize := readUnbounded(query, "page_size", defaultTitlesPageSize, &invalid)
	if len(invalid) > 0 {
		r.Errors.WriteValidation(response, requestID, invalid)
		return
	}

	// `offset = page * page_size` and `.limit(page_size + 1)`, in integers
	// with no width, and then the driver's question: does each fit a bigint.
	offset := new(big.Int).Mul(page, pageSize)
	limit := new(big.Int).Add(pageSize, big.NewInt(1))
	if !offset.IsInt64() || !limit.IsInt64() {
		failed.caught(offsetTooWide, errTooWide)
		return
	}

	var rows []TitleRow
	var err error
	term := pythontext.Strip(title)
	if utf8.RuneCountInString(term) < shortestSearch {
		rows, err = r.Titles.RecentTitles(ctx.Context(), caller.UserID, limit.Int64(), offset.Int64())
	} else {
		rows, err = r.Titles.MatchingTitles(ctx.Context(), caller.UserID, term, limit.Int64(), offset.Int64())
	}
	if err != nil {
		failed.caught("could not read the documents' titles", err)
		return
	}

	// `has_more = len(rows) > page_size` and `items = rows[:page_size]`.
	more := big.NewInt(int64(len(rows))).Cmp(pageSize) > 0
	rows = rows[:pythonSliceEnd(len(rows), pageSize)]

	items := make([]any, 0, len(rows))
	for _, row := range rows {
		// DocumentTitleRead: id, title, document_type, folder_id. The route
		// selects three columns and builds the model from those, so
		// `folder_id` is the model's default for a document that is in a
		// folder too.
		item := pythonjson.NewObject()
		item.Set("id", row.ID)
		item.Set("title", row.Title)
		item.Set("document_type", row.DocumentType)
		item.Set("folder_id", nil)
		items = append(items, item)
	}
	body := pythonjson.NewObject()
	body.Set("items", items)
	body.Set("has_more", more)
	encoded, err := pythonjson.Marshal(body)
	if err != nil {
		failed.uncaught(answerUnrendered, err)
		return
	}
	pythonjson.Send(response, http.StatusOK, encoded)
}

// readUnbounded reads a query parameter typed `int` with a default and no
// bound, as an integer of any width: the route multiplies and adds with it
// before any statement sees it, and Python does that exactly.
func readUnbounded(query starletteQuery, name string, fallback int64, failures *[]errorenvelope.Failure) *big.Int {
	text, named := query.value(name)
	if !named {
		return big.NewInt(fallback)
	}
	digits, readable := requestnumber.WholeNumber(text)
	if !readable {
		*failures = append(*failures, errorenvelope.Failure{
			Location: []string{"query", name},
			Message:  errorenvelope.IntegerExpected,
		})
		return new(big.Int)
	}
	// WholeNumber has checked the syntax: a sign and decimal digits.
	value, _ := new(big.Int).SetString(digits, 10)
	return value
}

// pythonSliceEnd is where `rows[:end]` stops for a list of the given length:
// at end when it is inside the list, at the list's own end when it is past
// it, and -- for a negative end -- that many short of the list's end, and
// never before its start.
func pythonSliceEnd(length int, end *big.Int) int {
	size := big.NewInt(int64(length))
	if end.Sign() < 0 {
		end = new(big.Int).Add(size, end)
		if end.Sign() < 0 {
			return 0
		}
	}
	if end.Cmp(size) > 0 {
		return length
	}
	return int(end.Int64())
}
