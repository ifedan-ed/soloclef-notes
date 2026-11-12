// Package activitylogs serves the single-entry routes of the account's
// activity log: what background work did for the account and how it ended.
//
//	POST   /api/v1/logs            write an entry
//	GET    /api/v1/logs/{log_id}   read one
//	PUT    /api/v1/logs/{log_id}   change one
//	DELETE /api/v1/logs/{log_id}   remove one
//
// The log is the account's own. Every statement names the owner
// (queries.sql), so an entry of another account is answered exactly as an
// entry that does not exist, and there is nothing else to check: no role, no
// permission. The summary of a recent window is internal/activitysummary.
package activitylogs

import "time"

// Entry is a logs row as LogRead renders it.
type Entry struct {
	CreatedAt time.Time
	ID        int64
	Level     string
	Status    string
	Message   string
	Source    *string
	// Metadata is the log_metadata column's own bytes, or nil for none.
	Metadata []byte
}

// logAbsent is the 404 all three single-log routes share.
const logAbsent = "Log not found"

// The enum members, in the order app.db declares them, which is the order
// pydantic names them in its refusal.
var (
	levels   = []string{"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"}
	statuses = []string{"IN_PROGRESS", "SUCCESS", "FAILED"}
)
