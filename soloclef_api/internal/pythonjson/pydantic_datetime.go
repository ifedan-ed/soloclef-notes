package pythonjson

import (
	"fmt"
	"time"
)

// PydanticDateTime renders a moment as pydantic writes a datetime in JSON,
// which is what a route with a response_model returns — not isoformat().
//
// The two differ on the wire and clients read both: pydantic spells a zero
// offset "Z" rather than "+00:00", and pads the fraction to six digits whenever
// there is one. A route whose model declares a datetime field must use this;
// one that hands a datetime to jsonable_encoder must use ISOFormat.
//
// Timestamps arrive here as UTC, because asyncpg hands SQLAlchemy a UTC-aware
// datetime for a timestamptz column and pydantic formats that.
func PydanticDateTime(t time.Time) string {
	utc := t.UTC()
	text := utc.Format("2006-01-02T15:04:05")
	if microseconds := utc.Nanosecond() / 1000; microseconds != 0 {
		text += fmt.Sprintf(".%06d", microseconds)
	}
	return text + "Z"
}
