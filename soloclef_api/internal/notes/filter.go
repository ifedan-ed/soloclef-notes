package notes

import (
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// Filter is what the list was asked for.
type Filter struct {
	// Query is the search text, spaces collapsed; "" is no search.
	Query string
	// NotebookID is one notebook's notes; nil is every notebook's and none.
	NotebookID *int64
	// Unfiled is the notes in no notebook.
	Unfiled bool
	// PinnedOnly is the pinned notes alone.
	PinnedOnly bool
	// Origin is one origin's notes; nil is all of them.
	Origin *string
	// ThreadID is the notes kept from one conversation, which is how an
	// answer knows it has been saved.
	ThreadID *int64
	Sort     string
	Offset   int64
	Limit    int64
}

// The words of a refused query parameter.
const (
	notWholeNumber  = "Input should be a valid integer"
	notTruth        = "Input should be a valid boolean"
	tooLong         = "String should have at most 200 characters"
	badOrigin       = "Input should be 'hand', 'chat' or 'automation'"
	badSort         = "Input should be 'edited', 'created' or 'title'"
	pageTooLarge    = "Input should be less than or equal to 200"
	pageTooSmall    = "Input should be greater than or equal to 1"
	offsetNegative  = "Input should be greater than or equal to 0"
	filedAndUnfiled = "notebook_id and unfiled cannot both be given"
)

// ReadFilter reads the list's query parameters, reporting every bad one at
// once. An absent parameter is its default; a parameter given twice is read
// from its last value, as a form is.
func ReadFilter(query url.Values) (Filter, []errorenvelope.Failure) {
	filter := Filter{Sort: SortEdited, Limit: DefaultPageSize}
	var failures []errorenvelope.Failure
	refuse := func(name, message string) {
		failures = append(failures, errorenvelope.Failure{Location: []string{"query", name}, Message: message})
	}
	last := func(name string) (string, bool) {
		values, named := query[name]
		if !named || len(values) == 0 {
			return "", false
		}
		return values[len(values)-1], true
	}
	number := func(name string) *int64 {
		text, named := last(name)
		if !named {
			return nil
		}
		value, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			refuse(name, notWholeNumber)
			return nil
		}
		return &value
	}
	truth := func(name string) bool {
		text, named := last(name)
		if !named {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(text)) {
		case "true", "1", "yes", "on":
			return true
		case "false", "0", "no", "off", "":
			return false
		}
		refuse(name, notTruth)
		return false
	}

	if text, named := last("q"); named {
		filter.Query = strings.Join(strings.Fields(text), " ")
		if utf8.RuneCountInString(filter.Query) > MaxQueryLength {
			refuse("q", tooLong)
		}
	}
	filter.NotebookID = number("notebook_id")
	filter.Unfiled = truth("unfiled")
	filter.PinnedOnly = truth("pinned")
	if text, named := last("origin"); named && text != "" {
		switch text {
		case OriginHand, OriginChat, OriginAutomation:
			filter.Origin = &text
		default:
			refuse("origin", badOrigin)
		}
	}
	filter.ThreadID = number("thread_id")
	if text, named := last("sort"); named && text != "" {
		switch text {
		case SortEdited, SortCreated, SortTitle:
			filter.Sort = text
		default:
			refuse("sort", badSort)
		}
	}
	if limit := number("limit"); limit != nil {
		switch {
		case *limit < 1:
			refuse("limit", pageTooSmall)
		case *limit > MaxPageSize:
			refuse("limit", pageTooLarge)
		default:
			filter.Limit = *limit
		}
	}
	if offset := number("offset"); offset != nil {
		if *offset < 0 {
			refuse("offset", offsetNegative)
		} else {
			filter.Offset = *offset
		}
	}
	// Both at once is a list of nothing, which is a mistake in the caller
	// worth saying out loud rather than an empty page.
	if filter.NotebookID != nil && filter.Unfiled {
		refuse("unfiled", filedAndUnfiled)
	}
	return filter, failures
}

// titlePattern is the ILIKE pattern for a title containing the search text.
// The text's own wildcards and the escape character are escaped, so a
// search for "50%" matches "50%" and not every title holding "50".
func titlePattern(query string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(query)
	return "%" + escaped + "%"
}
