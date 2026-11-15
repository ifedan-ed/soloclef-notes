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
		// The Status tab holds what ends for a person too: Studio outputs,
		// automation runs, mail drafts and an App Store update.
		{"a tab is its types", "category=status", func(v view) bool { return len(v.types) == 7 }},
		{"a Studio output is a status type", "category=status&type=studio_output", func(v view) bool {
			return len(v.types) == 1 && v.types[0] == "studio_output"
		}},
		{"a type inside its tab", "category=status&type=connector_deletion", func(v view) bool {
			return len(v.types) == 1 && v.types[0] == "connector_deletion"
		}},
		{"a connector source", "source_type=connector:GOOGLE_DRIVE_CONNECTOR", func(v view) bool {
			return v.sourceKey == "connector_type" && v.sourceValue == "GOOGLE_DRIVE_CONNECTOR" && len(v.types) == 2
		}},
		{"a document source keeps a colon in its value", "source_type=doctype:A:B", func(v view) bool {
			return v.sourceKey == "document_type" && v.sourceValue == "A:B"
		}},
		{"filters with no type in common match nothing", "type=document_processing&source_type=connector:GOOGLE_DRIVE_CONNECTOR", func(v view) bool {
			return v.none
		}},
		{"filters with a type in common", "type=connector_indexing&source_type=connector:GOOGLE_DRIVE_CONNECTOR", func(v view) bool {
			return !v.none && len(v.types) == 1 && v.types[0] == "connector_indexing"
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
		// The administrator's announcements were removed: their type is a
		// name the inbox no longer knows. (Their tab is still read, as an
		// empty view, for the installed phone build:
		// TestTheRetiredAnnouncementsTabIsEmpty.)
		{"type=site_announcement", "type"},
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

// iPhone 1.1.0 from the App Store still asks for the retired announcements
// tab; it must be an empty view, never every row.
func TestTheRetiredAnnouncementsTabIsEmpty(t *testing.T) {
	got, failures := readView(url.Values{"category": {"announcements"}}, true)
	if len(failures) != 0 {
		t.Fatalf("failures %v", failures)
	}
	if !got.none {
		t.Fatalf("%+v, want a view that matches nothing", got)
	}
	if _, failures := readView(url.Values{"category": {"news"}}, true); len(failures) != 1 {
		t.Fatalf("an unknown category must still be refused, got %v", failures)
	}
}
