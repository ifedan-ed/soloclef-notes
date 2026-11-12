package notes

import (
	"net/url"
	"reflect"
	"testing"
)

func TestTheListReadsItsDefaultsWhenAskedForNothing(t *testing.T) {
	filter, failures := ReadFilter(url.Values{})
	if len(failures) > 0 {
		t.Fatalf("failures %v", failures)
	}
	want := Filter{Sort: SortEdited, Limit: DefaultPageSize}
	if !reflect.DeepEqual(filter, want) {
		t.Errorf("filter = %+v, want %+v", filter, want)
	}
}

func TestTheListReadsEveryFilter(t *testing.T) {
	filter, failures := ReadFilter(url.Values{
		"q":           {"  dose   of\tamoxicillin "},
		"notebook_id": {"7"},
		"pinned":      {"true"},
		"origin":      {"chat"},
		"thread_id":   {"12"},
		"sort":        {"title"},
		"limit":       {"20"},
		// The last value of a repeated parameter is the one read.
		"offset": {"5", "40"},
	})
	if len(failures) > 0 {
		t.Fatalf("failures %v", failures)
	}
	if filter.Query != "dose of amoxicillin" {
		t.Errorf("query %q: spaces are collapsed", filter.Query)
	}
	if *filter.NotebookID != 7 || !filter.PinnedOnly || *filter.Origin != "chat" || *filter.ThreadID != 12 {
		t.Errorf("filter %+v", filter)
	}
	if filter.Sort != SortTitle || filter.Limit != 20 || filter.Offset != 40 {
		t.Errorf("sort %q, limit %d, offset %d", filter.Sort, filter.Limit, filter.Offset)
	}
}

func TestEveryBadParameterIsReportedAtOnce(t *testing.T) {
	_, failures := ReadFilter(url.Values{
		"notebook_id": {"seven"},
		"pinned":      {"maybe"},
		"origin":      {"dream"},
		"sort":        {"size"},
		"limit":       {"500"},
		"offset":      {"-1"},
	})
	var named []string
	for _, failure := range failures {
		named = append(named, failure.Location[1])
	}
	want := []string{"notebook_id", "pinned", "origin", "sort", "limit", "offset"}
	if !reflect.DeepEqual(named, want) {
		t.Errorf("refused %v, want %v", named, want)
	}
}

func TestOneNotebookAndNoNotebookCannotBothBeAskedFor(t *testing.T) {
	_, failures := ReadFilter(url.Values{"notebook_id": {"3"}, "unfiled": {"1"}})
	if len(failures) != 1 || failures[0].Location[1] != "unfiled" {
		t.Errorf("failures %v", failures)
	}
}

func TestASearchLongerThanTheLimitIsRefused(t *testing.T) {
	long := make([]rune, MaxQueryLength+1)
	for index := range long {
		long[index] = 'é'
	}
	if _, failures := ReadFilter(url.Values{"q": {string(long)}}); len(failures) != 1 {
		t.Errorf("a %d-character search was not refused", len(long))
	}
	if _, failures := ReadFilter(url.Values{"q": {string(long[:MaxQueryLength])}}); len(failures) != 0 {
		t.Errorf("a search of exactly the limit was refused: %v", failures)
	}
}

func TestTheTitlePatternMatchesWhatWasTypedAndNothingMore(t *testing.T) {
	cases := map[string]string{
		"dose":     `%dose%`,
		"50%":      `%50\%%`,
		"a_b":      `%a\_b%`,
		`back\sl`:  `%back\\sl%`,
		"plain ok": `%plain ok%`,
	}
	for typed, want := range cases {
		if got := titlePattern(typed); got != want {
			t.Errorf("titlePattern(%q) = %q, want %q", typed, got, want)
		}
	}
}
