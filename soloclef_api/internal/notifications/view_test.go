package notifications

import (
	"net/url"
	"strings"
	"testing"
)

func TestAViewIsReadFromTheFiltersATabSends(t *testing.T) {
	for _, c := range []struct {
		name, query string
		check       func(view) bool
	}{
		{"nothing is the whole inbox, fifty a page", "", func(v view) bool {
			return v.types == nil && v.limit == 50 && v.offset == 0 && v.before == nil && v.searchPattern == ""
		}},
		{"a tab is its types", "category=status", func(v view) bool { return len(v.types) == 3 }},
		{"a type inside its tab", "category=status&type=connector_deletion", func(v view) bool {
			return len(v.types) == 1 && v.types[0] == "connector_deletion"
		}},
		{"a type outside the tab is an empty view, not the tab", "category=announcements&type=connector_deletion", func(v view) bool {
			return len(v.types) == 1 && !knownType(v.types[0])
		}},
		{"a connector source", "source_type=connector:GOOGLE_DRIVE_CONNECTOR", func(v view) bool {
			return v.sourceKey == "connector_type" && v.sourceValue == "GOOGLE_DRIVE_CONNECTOR" && len(v.types) == 2
		}},
		{"a document source keeps a colon in its value", "source_type=doctype:A:B", func(v view) bool {
			return v.sourceKey == "document_type" && v.sourceValue == "A:B"
		}},
		{"the presets", "filter=errors", func(v view) bool { return v.errorsOnly && !v.unreadOnly }},
		{"a search matches its own characters", "search=" + url.QueryEscape(` 100%_a\ `), func(v view) bool {
			return v.searchPattern == `%100\%\_a\\%`
		}},
		{"paging", "limit=100&offset=20&before_date=2026-11-12T09:00:00.123456Z", func(v view) bool {
			return v.limit == 100 && v.offset == 20 && v.before != nil && v.before.Nanosecond() == 123456000
		}},
		{"a date as Python wrote it, with no offset", "before_date=2026-11-12T09:00:00.123456", func(v view) bool {
			return v.before != nil && v.before.UTC().Hour() == 9
		}},
	} {
		values, _ := url.ParseQuery(c.query)
		got, failures := readView(values, true)
		if len(failures) > 0 || !c.check(got) {
			t.Errorf("%s: %+v, failures %v", c.name, got, failures)
		}
	}
}

func TestAFilterThatIsNotUnderstoodIsRefusedByName(t *testing.T) {
	for _, c := range []struct{ query, names string }{
		{"type=new_mention", "type"},
		{"category=comments", "category"},
		{"source_type=GOOGLE_DRIVE_CONNECTOR", "source_type"},
		{"source_type=connector:", "source_type"},
		{"filter=failed", "filter"},
		{"search=" + strings.Repeat("a", 201), "search"},
		{"before_date=yesterday", "before_date"},
		{"limit=0", "limit"}, {"limit=101", "limit"}, {"limit=ten", "limit"},
		{"offset=-1", "offset"},
		{"limit=0&offset=-1&filter=x", "limit offset filter"},
	} {
		values, _ := url.ParseQuery(c.query)
		_, failures := readView(values, true)
		named := map[string]bool{}
		for _, failure := range failures {
			named[failure.Location[1]] = true
		}
		for _, name := range strings.Fields(c.names) {
			if !named[name] {
				t.Errorf("%s: %s was not refused (%v)", c.query, name, failures)
			}
		}
	}
	// The clear and the counts take no paging, so a limit there is not read.
	values, _ := url.ParseQuery("limit=0")
	if _, failures := readView(values, false); len(failures) != 0 {
		t.Errorf("paging was checked where it does not apply: %v", failures)
	}
}
