package pythonjson

import (
	"fmt"
	"time"
)

// ISOFormat renders a moment as Python's datetime.isoformat(), which is what
// FastAPI's jsonable_encoder writes for a datetime: seconds always, the
// fraction only when there is one, and the UTC offset spelled "+00:00".
//
// Timestamps always arrive here as UTC, because asyncpg hands SQLAlchemy a
// UTC-aware datetime for a timestamptz column and Python formats that.
func ISOFormat(t time.Time) string {
	utc := t.UTC()
	text := utc.Format("2006-01-02T15:04:05")
	if microseconds := utc.Nanosecond() / 1000; microseconds != 0 {
		text += fmt.Sprintf(".%06d", microseconds)
	}
	return text + "+00:00"
}
