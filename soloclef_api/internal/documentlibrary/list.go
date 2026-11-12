package documentlibrary

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// filter is a list request, read and checked.
type filter struct {
	types    []string
	unfiled  bool
	folderID *int64
	// pattern is the ILIKE pattern of a title search; nil for the list.
	pattern    *string
	sort       string
	descending bool
	page       int64
	pageSize   int64
	skip       int64
}

// escapeLike makes a search term match itself: `%`, `_` and the escape
// character are ILIKE's own, and a title search for "100%" should find that
// title and not every title containing "100".
var escapeLike = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// readFilter reads the query, reporting every bad parameter at once.
// `search` says the request is the title search, which requires `title`,
// takes no folder and no sort, and answers in id order.
func readFilter(query url.Values, search bool) (filter, []errorenvelope.Failure) {
	read := filter{sort: "created_at", descending: true, pageSize: defaultPageSize}
	var failures []errorenvelope.Failure
	bad := func(name, message string) {
		failures = append(failures, errorenvelope.Failure{Location: []string{"query", name}, Message: message})
	}
	whole := func(name string, least, most int64, into *int64) bool {
		text := strings.TrimSpace(query.Get(name))
		if text == "" {
			return false
		}
		value, err := strconv.ParseInt(text, 10, 64)
		if err != nil || value < least || value > most {
			bad(name, "Input should be a whole number from "+strconv.FormatInt(least, 10)+" to "+strconv.FormatInt(most, 10))
			return false
		}
		*into = value
		return true
	}
	// The largest offset a bigint page can name without overflowing.
	const farthest = int64(1) << 40
	whole("page", 0, farthest/maxPageSize, &read.page)
	whole("page_size", 1, maxPageSize, &read.pageSize)
	read.skip = read.page * read.pageSize
	if whole("skip", 0, farthest, &read.skip) {
		// An absolute offset wins over the page, and the page answered is
		// the one that offset falls in.
		read.page = read.skip / read.pageSize
	}

	for _, name := range strings.Split(query.Get("document_types"), ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !documentTypes[name] {
			bad("document_types", strconv.Quote(name)+" is not a document type")
			continue
		}
		read.types = append(read.types, name)
	}
	if read.types == nil {
		// An empty array, not NULL: the statement asks its cardinality.
		read.types = []string{}
	}

	if search {
		term := strings.TrimSpace(query.Get("title"))
		if term == "" {
			bad("title", "Field required")
		}
		pattern := "%" + escapeLike.Replace(term) + "%"
		read.pattern, read.sort, read.descending = &pattern, "id", false
		return read, failures
	}

	switch folder := strings.ToLower(strings.TrimSpace(query.Get("folder_id"))); folder {
	case "":
	case "root", "null":
		read.unfiled = true
	default:
		id, err := strconv.ParseInt(folder, 10, 64)
		if err != nil {
			bad("folder_id", `Input should be a folder's id, or "root" for documents in no folder`)
			break
		}
		read.folderID = &id
	}
	switch sort := query.Get("sort_by"); sort {
	case "":
	case "created_at", "title", "document_type":
		read.sort = sort
	default:
		bad("sort_by", "Input should be 'created_at', 'title' or 'document_type'")
	}
	switch order := query.Get("sort_order"); order {
	case "", "desc":
	case "asc":
		read.descending = false
	default:
		bad("sort_order", "Input should be 'asc' or 'desc'")
	}
	return read, failures
}

func (r Routes) list(x *exchange)   { r.answerPage(x, false) }
func (r Routes) search(x *exchange) { r.answerPage(x, true) }

// answerPage answers a page for the list and for the title search, which
// are one statement with and without a title pattern.
func (r Routes) answerPage(x *exchange, search bool) {
	address := x.ctx.URL()
	asked, failures := readFilter(address.Query(), search)
	if len(failures) > 0 {
		x.invalid(failures...)
		return
	}
	ctx := x.ctx.Context()
	queries := New(r.Pool)
	rows, err := queries.DocumentPage(ctx, DocumentPageParams{
		PreviewChars: previewChars, UserID: x.userID, DocumentTypes: asked.types, Unfiled: asked.unfiled,
		FolderID: asked.folderID, TitlePattern: asked.pattern, Sort: asked.sort, Descending: asked.descending,
		Skip: asked.skip, PageSize: asked.pageSize,
	})
	if err != nil {
		x.fail("could not read the documents", err)
		return
	}
	answer := page{Items: make([]document, 0, len(rows)), Page: asked.page, PageSize: asked.pageSize}
	for _, row := range rows {
		answer.Items = append(answer.Items, fromPageRow(row))
		answer.Total = row.Total
	}
	if len(rows) == 0 && asked.skip > 0 {
		// A page past the end has no row to carry the count, and "total: 0"
		// would tell a client paging to the end that the library is empty.
		answer.Total, err = queries.DocumentCount(ctx, DocumentCountParams{
			UserID: x.userID, DocumentTypes: asked.types, Unfiled: asked.unfiled,
			FolderID: asked.folderID, TitlePattern: asked.pattern,
		})
		if err != nil {
			x.fail("could not count the documents", err)
			return
		}
	}
	answer.HasMore = asked.skip+int64(len(rows)) < answer.Total
	x.send(http.StatusOK, answer)
}
