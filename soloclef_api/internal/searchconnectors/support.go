package searchconnectors

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// Rows reads an account's connections. Every method takes the owner, and the
// statement behind each names it (queries.sql): there is no way to ask this
// interface about a connection without saying whose it must be.
type Rows interface {
	// Connection is one connection of any type, or nil when it is not the
	// owner's or is not there. The two are deliberately one answer.
	Connection(ctx context.Context, ownerID string, connectorID int64) (*Connection, error)
	// Server is the same for an MCP server; a connection of another type is
	// nil.
	Server(ctx context.Context, ownerID string, connectorID int64) (*Connection, error)
	// Connections is one page of the owner's connections, by name then id.
	Connections(ctx context.Context, ownerID string, skip, limit int32) ([]Connection, error)
	// Servers is the owner's MCP servers, in the same order.
	Servers(ctx context.Context, ownerID string) ([]Connection, error)
}

// Changes are the writes to one connection that are a single decision each.
type Changes interface {
	// SetEnabled switches the owner's connection on or off and returns it,
	// or nil when it is not the owner's or is not there.
	SetEnabled(ctx context.Context, ownerID string, connectorID int64, enabled bool) (*Connection, error)
	// DeleteServer removes the owner's MCP server and reports whether there
	// was one.
	DeleteServer(ctx context.Context, ownerID string, connectorID int64) (bool, error)
	// EditTrustedTools reads the server's trusted-tool list, hands it to
	// edit, and stores what edit returns when edit says it changed. The read
	// and the write are one transaction with the row locked between them.
	// found is false when the server is not the owner's or is not there.
	EditTrustedTools(ctx context.Context, ownerID string, connectorID int64,
		edit func(trusted []string) (next []string, changed bool)) (trusted []string, stored, found bool, err error)
}

// number is a whole-number parameter: its value, and whether it was a number
// too wide for any column. A too-wide id names no row, so the routes answer
// it as "not found" and send no statement.
type number struct {
	value   int64
	tooWide bool
}

// readNumber reads a parameter and adds its refusal to failures if it is not
// a whole number.
func readNumber(text string, failures *[]errorenvelope.Failure, location ...string) number {
	value, failure, rangeErr := requestnumber.Parse(text, location...)
	if failure != nil {
		*failures = append(*failures, *failure)
	}
	return number{value: value, tooWide: rangeErr != nil}
}

// failures is how a route of this package writes its sanitised 500: the
// reason goes to the log with the request id and never outward.
type failures struct {
	ctx       huma.Context
	errors    errorenvelope.Writer
	logger    *slog.Logger
	response  http.ResponseWriter
	requestID string
	message   string
}

func (f failures) log(reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", f.requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		// A driver's error or one of this package's own. Neither quotes a
		// row, so no stored credential can reach the log through here.
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	f.logger.LogAttrs(f.ctx.Context(), slog.LevelError, f.message, attributes...)
}

// caught is the 500 for a failure before the answer was being written.
func (f failures) caught(reason string, cause error) {
	f.log(reason, cause)
	f.errors.Write(f.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, f.requestID)
}

// uncaught is the 500 for a failure while the answer was being written, which
// goes out without CORS headers (internal/corsmirror.Suppress).
func (f failures) uncaught(reason string, cause error) {
	f.log(reason, cause)
	corsmirror.Suppress(f.ctx.Context())
	f.errors.Write(f.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, f.requestID)
}

// The reasons a 500 of this package is logged with.
const (
	noCaller      = "the route ran without an authenticated caller"
	rowUnread     = "could not read the connection"
	rowRefused    = "a stored connection could not be rendered"
	bodyUnwritten = "could not render the response"
)
