package pushpolicy

import (
	"errors"
	"fmt"
	"strings"
	"time"
	// The time zone database, compiled in. The API and worker images are a
	// static binary with no /usr/share/zoneinfo, and without this every zone
	// but UTC would be refused at save and unreadable at send. It is here,
	// not in each main package, because quiet hours are wrong in any process
	// that forgets it, and this is the one package that needs it.
	_ "time/tzdata"
)

// minutesPerDay bounds a clock reading: 0 is midnight, 1439 is 23:59.
const minutesPerDay = 24 * 60

// QuietHours is a span of the day in the person's own time zone in which
// nothing is pushed. The span may cross midnight (22:00 to 07:00), which is
// the usual case. The zero value is not a span; a person without quiet hours
// has a nil *QuietHours.
type QuietHours struct {
	// StartMinute and EndMinute are minutes since local midnight; the span
	// includes its start and excludes its end, so 22:00-07:00 is quiet at
	// 06:59 and not at 07:00.
	StartMinute int
	EndMinute   int
	// Zone is the IANA name the person chose ("Africa/Lagos"), kept as
	// text so it can be saved and shown exactly as chosen.
	Zone string
	// location is Zone, loaded once when the span was made.
	location *time.Location
}

// Errors a person's input can earn, each said in so many words.
var (
	ErrClock    = errors.New(`a time must be written "HH:MM", from 00:00 to 23:59`)
	ErrEmpty    = errors.New("quiet hours that start when they end are either nothing or the whole day; choose two different times")
	ErrZoneName = errors.New("not a time zone name (an IANA name such as Africa/Lagos)")
)

// NewQuietHours checks and builds a span from the two minutes and the zone.
// Nothing is guessed: a zone the time zone database does not know is
// refused, not read as UTC.
func NewQuietHours(startMinute, endMinute int, zone string) (QuietHours, error) {
	switch {
	case startMinute < 0 || startMinute >= minutesPerDay || endMinute < 0 || endMinute >= minutesPerDay:
		return QuietHours{}, ErrClock
	case startMinute == endMinute:
		return QuietHours{}, ErrEmpty
	}
	location, err := loadZone(zone)
	if err != nil {
		return QuietHours{}, err
	}
	return QuietHours{StartMinute: startMinute, EndMinute: endMinute, Zone: zone, location: location}, nil
}

// loadZone accepts an IANA name only. time.LoadLocation also takes "" and
// "Local", which mean the server's zone, not the person's: either would put
// their quiet hours in a zone nobody chose.
func loadZone(zone string) (*time.Location, error) {
	if zone == "" || zone == "Local" || len(zone) > 64 || strings.ContainsAny(zone, " \t\n") {
		return nil, ErrZoneName
	}
	location, err := time.LoadLocation(zone)
	if err != nil {
		return nil, ErrZoneName
	}
	return location, nil
}

// Covers reports whether the instant falls inside the span, read on the
// wall clock of the person's zone. A summer-time change is handled by the
// zone itself: 22:00 is 22:00 local on both sides of it.
func (q QuietHours) Covers(instant time.Time) bool {
	local := instant.In(q.location)
	minute := local.Hour()*60 + local.Minute()
	if q.StartMinute < q.EndMinute {
		return minute >= q.StartMinute && minute < q.EndMinute
	}
	// Across midnight: quiet from the start to midnight, and from midnight
	// to the end.
	return minute >= q.StartMinute || minute < q.EndMinute
}

// ParseClock reads "HH:MM" (24-hour, two digits each) as minutes since
// midnight. "7:00", "24:00" and "07:00:00" are refused: one spelling, so a
// stored value and a shown one are always the same text.
func ParseClock(text string) (int, error) {
	if len(text) != 5 || text[2] != ':' || !isDigits(text[:2]) || !isDigits(text[3:]) {
		return 0, ErrClock
	}
	hours := int(text[0]-'0')*10 + int(text[1]-'0')
	minutes := int(text[3]-'0')*10 + int(text[4]-'0')
	if hours > 23 || minutes > 59 {
		return 0, ErrClock
	}
	return hours*60 + minutes, nil
}

func isDigits(text string) bool {
	for i := 0; i < len(text); i++ {
		if text[i] < '0' || text[i] > '9' {
			return false
		}
	}
	return true
}

// FormatClock writes minutes since midnight as ParseClock reads them.
func FormatClock(minute int) string {
	return fmt.Sprintf("%02d:%02d", minute/60, minute%60)
}
