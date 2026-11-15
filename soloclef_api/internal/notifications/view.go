package notifications

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// view is the filters of one inbox view, checked. The zero value is the
// caller's whole inbox.
type view struct {
	// types is the types the view keeps; nil keeps every type. A `type`,
	// a `category` and a `source_type` together keep their intersection.
	types []string
	// none says the view matches no row: filters with no type in common, or
	// the retired announcements tab. The routes answer it without a query.
	// It is a flag, not a made-up type in types, because a placeholder type
	// has to reach Postgres to match nothing, and the one used before
	// ("\x00none") was refused there (a NUL is not valid text), so every
	// such view was a 500 (2026-11-13).
	none bool
	// sourceKey is the metadata key a source filter compares ("" for no
	// filter) and sourceValue what it must equal.
	sourceKey, sourceValue string
	unreadOnly, errorsOnly bool
	// searchPattern is the ILIKE pattern, or "".
	searchPattern string
	before        *time.Time
	limit, offset int
}

// likeEscaper makes a search term match itself: ILIKE reads %, _ and the
// backslash as its own syntax.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// The spellings of a timestamp a client may page from: this tier's own
// (RFC 3339) and Python's isoformat, which an older cached page may hold.
var beforeLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02"}

// readView reads a view from a query string and reports every problem at
// once. paging says whether limit, offset and before_date belong: the list
// takes them, the clear and the counts do not.
func readView(query url.Values, paging bool) (view, []errorenvelope.Failure) {
	var failures []errorenvelope.Failure
	fail := func(name, message string) {
		failures = append(failures, errorenvelope.Failure{Location: []string{"query", name}, Message: message})
	}
	out := view{limit: defaultPageSize}

	if name := query.Get("type"); name != "" {
		if !contains(statusTypes, name) {
			fail("type", "type must be one of "+strings.Join(statusTypes, ", "))
		}
		out.types = []string{name}
	}
	if name := query.Get("category"); name != "" {
		switch name {
		case statusCategory:
			out.narrow(statusTypes)
		case retiredAnnouncementsCategory:
			// Announcements are gone, but the phone builds 1.0.0 and 1.1.0 in
			// the App Store still ask for (and clear) this tab every time the
			// Inbox opens and show an error on a 422. It is an empty view:
			// no rows, nothing to clear.
			// installed-builds: notifications-announcements-tab
			out.none = true
		default:
			fail("category", "category must be "+statusCategory)
		}
	}
	if source := query.Get("source_type"); source != "" {
		kind, value, found := strings.Cut(source, ":")
		var of []string
		switch {
		case found && kind == "connector" && value != "":
			out.sourceKey, of = "connector_type", []string{"connector_indexing", "connector_deletion"}
		case found && kind == "doctype" && value != "":
			out.sourceKey, of = "document_type", []string{"document_processing"}
		default:
			fail("source_type", "source_type must be connector:<type> or doctype:<type>")
		}
		out.sourceValue = value
		if of != nil {
			out.narrow(of)
		}
	}
	switch preset := query.Get("filter"); preset {
	case "":
	case "unread":
		out.unreadOnly = true
	case "errors":
		out.errorsOnly = true
	default:
		fail("filter", "filter must be unread or errors")
	}
	if term := strings.TrimSpace(query.Get("search")); term != "" {
		if utf8.RuneCountInString(term) > maxSearchChars || strings.ContainsRune(term, 0) {
			fail("search", fmt.Sprintf("search must be at most %d characters", maxSearchChars))
		}
		out.searchPattern = "%" + likeEscaper.Replace(term) + "%"
	}
	if !paging {
		return out, failures
	}
	if text := query.Get("before_date"); text != "" {
		parsed, ok := parseBefore(text)
		if !ok {
			fail("before_date", "before_date must be an ISO date and time, for example 2026-01-15T00:00:00Z")
		}
		out.before = &parsed
	}
	if text := query.Get("limit"); text != "" {
		n, err := strconv.Atoi(text)
		if err != nil || n < 1 || n > maxPageSize {
			fail("limit", fmt.Sprintf("limit must be from 1 to %d", maxPageSize))
		}
		out.limit = n
	}
	if text := query.Get("offset"); text != "" {
		// The bound keeps offset+limit inside an int32, which is what the
		// statement takes.
		n, err := strconv.Atoi(text)
		if err != nil || n < 0 || n > 1_000_000_000 {
			fail("offset", "offset must be a whole number of rows to skip, 0 or more")
		}
		out.offset = n
	}
	return out, failures
}

func parseBefore(text string) (time.Time, bool) {
	for _, layout := range beforeLayouts {
		// A time with no offset is UTC: every timestamp this tier answers is.
		if parsed, err := time.ParseInLocation(layout, text, time.UTC); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func contains(list []string, name string) bool {
	for _, item := range list {
		if item == name {
			return true
		}
	}
	return false
}

// narrow keeps only the types also in of. A view left with no type in
// common matches nothing, which is the none flag, never an empty types (an
// empty list means every type to the statements).
func (v *view) narrow(of []string) {
	if v.types == nil {
		v.types = of
		return
	}
	kept := []string{}
	for _, name := range v.types {
		if contains(of, name) {
			kept = append(kept, name)
		}
	}
	v.types = kept
	if len(kept) == 0 {
		v.none = true
	}
}

// typesArgument is the view's types as the statements take them: never nil,
// because a nil slice is a SQL NULL and cardinality(NULL) is not 0.
func (v view) typesArgument() []string {
	if v.types == nil {
		return []string{}
	}
	return v.types
}
