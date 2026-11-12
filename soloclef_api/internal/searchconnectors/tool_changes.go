package searchconnectors

import (
	"context"
	"log/slog"
	"time"
)

// ToolChanges tells every process that offers an account's MCP tools to a
// model that they have changed. The chat caches tool definitions per account
// and drops them when a counter in Valkey moves (soloclef_assistant/mcptools,
// which is the implementation cmd/api passes here).
type ToolChanges interface {
	Bump(ctx context.Context, userID string) error
}

// bumpTimeout bounds the one Valkey command a change costs.
const bumpTimeout = 5 * time.Second

// toolsChanged moves the account's counter, after the change has committed.
//
// It cannot fail the request: the change is stored, and saying it was not
// would be untrue. If the counter cannot be moved, the other processes keep
// their cached definitions until they expire; what a call may do is still
// read from the row at each call (the address, the key, the trusted list,
// whether the server is switched on), so nothing stale is acted on. The
// request's own context is not used, because a client that has gone away
// must not stop the others being told.
func toolsChanged(ctx context.Context, tools ToolChanges, logger *slog.Logger, requestID, userID string) {
	bumping, done := context.WithTimeout(context.WithoutCancel(ctx), bumpTimeout)
	defer done()
	if err := tools.Bump(bumping, userID); err != nil {
		logger.LogAttrs(ctx, slog.LevelError,
			"an account's MCP tools changed and the other processes could not be told; each keeps its cached tool list until it expires",
			slog.String("request_id", requestID), slog.String("user_id", userID), slog.String("error", err.Error()))
	}
}
