// Package workspacelogs serves the four single-log routes of
// app/routes/logs_routes.py: the one read and the three writes that sit beside
// the log summary this tier already answers.
//
//	POST   /api/v1/logs            create_log
//	GET    /api/v1/logs/{log_id}   read_log
//	PUT    /api/v1/logs/{log_id}   update_log
//	DELETE /api/v1/logs/{log_id}   delete_log
//
// They were taken together because they are one slice: one table, one renderer,
// one pair of gates, and nothing else in the way. The inventory in
// custom/docs/go-api-tier.md puts all four in the hundred writes and reads that
// owe nothing outside their own rows — no Celery message, no notification, no
// push, no PostHog capture, no email.
//
// GET /api/v1/logs, the list beside them, is deliberately **not** here. Its
// start_date and end_date parameters are read by pydantic's own date parser,
// which accepts a dozen spellings and reports where in the text it stopped
// ("input is too short", "invalid character in ..."); reproducing that is a
// parser rather than a mirror, which is the same line this tier already draws
// at a request body it cannot decode (internal/requestbody).
package workspacelogs

import (
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/workspaceaccess"
)

// Access reads the caller's standing in the workspace a log belongs to. It is
// workspaceaccess.Rows under this slice's own name, so a route reads it from
// the pool and a write reads it from its own transaction.
type Access = workspaceaccess.Rows

// Entry is a logs row as LogRead renders it.
type Entry struct {
	CreatedAt time.Time
	ID        int64
	Level     string
	Status    string
	Message   string
	Source    *string
	// Metadata is the log_metadata column's own bytes, kept as text so the key
	// order survives into the response — which is what the Python route's round
	// trip through json.loads and pydantic preserves.
	Metadata    []byte
	WorkspaceID int64
}

// The permission each route asks for, and the Python routes' own wording for a
// member who does not hold it. create_log asks for logs:create and update_log
// for logs:update: they asked for logs:read until 2026-11-08, so anyone who
// could read a workspace's activity log could also append to it and rewrite
// what was already there.
const (
	writeDenied  = "You don't have permission to write logs in this workspace"
	readDenied   = "You don't have permission to read logs in this workspace"
	deleteDenied = "You don't have permission to delete logs in this workspace"
)

// logAbsent is the 404 all three single-log routes share.
const logAbsent = "Log not found"

// The enum members, in the order app.db declares them, which is the order
// pydantic names them in its refusal.
var (
	levels   = []string{"DEBUG", "INFO", "WARNING", "ERROR", "CRITICAL"}
	statuses = []string{"IN_PROGRESS", "SUCCESS", "FAILED"}
)
