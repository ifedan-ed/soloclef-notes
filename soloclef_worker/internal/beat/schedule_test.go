package beat

import (
	"testing"
	"time"
)

// A tick is published where the routing table sends its task, so a
// consumer given that queue sees it.
func TestATickIsRoutedAsEveryProducerRoutesItsTask(t *testing.T) {
	saved := scheduled
	defer func() { scheduled = saved }()
	scheduled = append(scheduled[:0:0],
		struct {
			name    string
			task    string
			cron    Crontab
			expires time.Duration
		}{"probe-heavy", "podcast.draft_transcript", Crontab{Minute: "*"}, time.Minute},
		struct {
			name    string
			task    string
			cron    Crontab
			expires time.Duration
		}{"probe-default", "notebook_autoname", Crontab{Minute: "*/5"}, time.Minute},
	)
	entries := Schedule("soloclef")
	if entries[0].Queue != "soloclef.heavy" || entries[1].Queue != "soloclef" {
		t.Fatalf("routed to %q and %q", entries[0].Queue, entries[1].Queue)
	}
}

// Every entry compiles and expires, or the beat refuses to start rather
// than silently skipping one.
func TestTheScheduleCompiles(t *testing.T) {
	for _, entry := range Schedule("soloclef") {
		if _, err := entry.Cron.Compile(); err != nil {
			t.Errorf("%s: %v", entry.Name, err)
		}
		if entry.Expires <= 0 {
			t.Errorf("%s has no expiry", entry.Name)
		}
	}
}
