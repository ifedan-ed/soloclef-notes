package delivery

import (
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_notify/pushsend"
)

// rowWith is a row's push to count phones of one sender.
func rowWith(id int64, count int, via route) outgoing {
	row := outgoing{notificationID: id, route: via}
	for i := 0; i < count; i++ {
		row.devices = append(row.devices, id*1000+int64(i))
		row.messages = append(row.messages, pushsend.Message{To: "t"})
	}
	return row
}

// Calls hold at most a hundred messages and never split a row's phones of
// one sender, so a failed call is retried for exactly its rows; each message
// keeps its row and phone, so a retry can name the phone.
func TestBatchesHoldWholeRowsUpToTheLimit(t *testing.T) {
	var rows []outgoing
	for id := int64(1); id <= 70; id++ {
		rows = append(rows, rowWith(id, 3, viaExpo))
	}
	batches := pack(rows)
	total := 0
	seen := map[int64]int{}
	for _, b := range batches {
		if len(b.messages) > pushsend.MaxBatch || len(b.rows) != len(b.messages) || len(b.devices) != len(b.messages) {
			t.Errorf("a batch of %d messages, %d rows, %d phones", len(b.messages), len(b.rows), len(b.devices))
		}
		for i, row := range b.rows {
			seen[row]++
			if b.devices[i]/1000 != row {
				t.Errorf("message %d of row %d names phone %d", i, row, b.devices[i])
			}
		}
		total += len(b.messages)
	}
	if total != 210 || len(seen) != 70 || len(batches) != 3 {
		t.Errorf("%d messages, %d rows, %d batches", total, len(seen), len(batches))
	}
	for id, times := range seen {
		if times != 3 {
			t.Errorf("row %d is in batches %d times for its 3 phones", id, times)
		}
	}
	if got := pack(nil); len(got) != 0 {
		t.Errorf("nothing to send made %d batches", len(got))
	}
	if huge := pack([]outgoing{rowWith(1, 150, viaExpo)}); len(huge) != 1 || len(huge[0].messages) != pushsend.MaxBatch || len(huge[0].devices) != pushsend.MaxBatch {
		t.Errorf("a row of 150 phones: %d batches", len(huge))
	}
}

// Each call is one sender's: a row's Expo and APNs phones go in calls of
// their own, and filling one sender's call does not close the other's.
func TestEachBatchIsOneSenders(t *testing.T) {
	rows := []outgoing{rowWith(1, 60, viaExpo), rowWith(1, 2, viaAPNs), rowWith(2, 60, viaExpo), rowWith(2, 1, viaAPNs)}
	batches := pack(rows)
	var expo, apple []batch
	for _, b := range batches {
		if b.route == viaAPNs {
			apple = append(apple, b)
		} else {
			expo = append(expo, b)
		}
	}
	if len(expo) != 2 || len(apple) != 1 || len(apple[0].messages) != 3 {
		t.Fatalf("expo %d calls, apns %d calls", len(expo), len(apple))
	}
	if apple[0].rows[0] != 1 || apple[0].rows[2] != 2 {
		t.Errorf("the APNs call's rows are %v", apple[0].rows)
	}
}

// The origin is the form the phone app compares; anything it could not be
// set to is refused, not guessed at.
func TestTheServerIsNamedAsThePhoneAppNamesIt(t *testing.T) {
	good := map[string]string{
		"https://Notes.Example.com/":         "https://notes.example.com",
		"https://notes.example.com:443/app":  "https://notes.example.com",
		"https://notes.example.com.":         "https://notes.example.com",
		"http://localhost:3945":              "http://localhost:3945",
		"http://host:80":                     "http://host",
		"https://xn--bcher-kva.example:8443": "https://xn--bcher-kva.example:8443",
	}
	for address, want := range good {
		if got, err := ServerOrigin(address); err != nil || got != want {
			t.Errorf("%q = %q, %v; want %q", address, got, err, want)
		}
	}
	for _, bad := range []string{"", "notes.example.com", "ftp://x", "https://", "https://[::1]:8080", "https://bücher.example"} {
		if got, err := ServerOrigin(bad); err == nil {
			t.Errorf("%q was accepted as %q", bad, got)
		}
	}
}
