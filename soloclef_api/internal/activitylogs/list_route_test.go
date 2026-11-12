package activitylogs

import (
	"net/url"
	"testing"
	"time"
)

func TestTheListFilterReadsWhatWasAskedAndRefusesWhatItCannotRead(t *testing.T) {
	read := func(raw string) (Filter, []string) {
		query, err := url.ParseQuery(raw)
		if err != nil {
			t.Fatal(err)
		}
		filter, failures := readFilter(query)
		var names []string
		for _, failure := range failures {
			names = append(names, failure.Location[1])
		}
		return filter, names
	}

	filter, refused := read("")
	if len(refused) != 0 || filter.PageSize != defaultPageSize || filter.Skip != 0 || filter.Level != nil || filter.NotAfter != nil {
		t.Errorf("nothing asked: %+v, refused %v", filter, refused)
	}
	// The form sends its untouched fields empty; that is not a filter.
	if filter, refused = read("level=&status=&source=&start_date=&limit="); len(refused) != 0 || filter.Level != nil || filter.Source != nil {
		t.Errorf("empty values: %+v, refused %v", filter, refused)
	}

	filter, refused = read("skip=20&limit=500&level=ERROR&status=FAILED&source=index&start_date=2026-11-10&end_date=2026-11-11")
	if len(refused) != 0 {
		t.Fatalf("a full filter was refused: %v", refused)
	}
	if filter.Skip != 20 || filter.PageSize != 500 || *filter.Level != "ERROR" || *filter.Status != "FAILED" || *filter.Source != "index" {
		t.Errorf("read %+v", filter)
	}
	if !filter.NotBefore.Equal(time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("the first day starts at %v", filter.NotBefore)
	}
	// "Until the 11th" includes the whole of the 11th.
	if want := time.Date(2026, 11, 11, 23, 59, 59, 999999000, time.UTC); !filter.NotAfter.Equal(want) {
		t.Errorf("the last day ends at %v, want %v", filter.NotAfter, want)
	}
	// A moment is that moment, with or without an offset.
	filter, _ = read("end_date=2026-11-11T09:30:00%2B01:00&start_date=2026-11-11T08:00:00")
	if !filter.NotAfter.Equal(time.Date(2026, 11, 11, 8, 30, 0, 0, time.UTC)) || !filter.NotBefore.Equal(time.Date(2026, 11, 11, 8, 0, 0, 0, time.UTC)) {
		t.Errorf("moments read as %v and %v", filter.NotBefore, filter.NotAfter)
	}

	// Everything unreadable is reported at once, by name.
	_, refused = read("skip=-1&limit=501&level=error&status=DONE&start_date=yesterday&end_date=11/11/2026")
	want := []string{"skip", "limit", "level", "status", "start_date", "end_date"}
	if len(refused) != len(want) {
		t.Fatalf("refused %v, want %v", refused, want)
	}
	for i := range want {
		if refused[i] != want[i] {
			t.Errorf("refused %v, want %v", refused, want)
		}
	}
	if _, refused = read("limit=0"); len(refused) != 1 {
		t.Errorf("a page of nothing was accepted")
	}
}
