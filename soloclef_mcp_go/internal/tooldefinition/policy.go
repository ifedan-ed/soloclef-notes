package tooldefinition

import "github.com/modelcontextprotocol/go-sdk/mcp"

// The policy hints a client reads to decide how freely it may call a tool. Each
// is spelled out rather than left to a default, because the Python server
// publishes all four on every tool and a missing one changes what a client sees.
var (
	// ReadOnlyClosed is a read of this deployment's own data.
	ReadOnlyClosed = annotations("", true, false, true, false)
	// ReadOnlyOpen is a read that reaches a third party.
	ReadOnlyOpen = annotations("", true, false, true, true)
	// WriteClosed changes this deployment's own data without destroying any.
	WriteClosed = annotations("", false, false, false, false)
	// DeleteClosed destroys this deployment's own data.
	DeleteClosed = annotations("", false, true, false, false)
)

func annotations(title string, readOnly, destructive, idempotent, openWorld bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    readOnly,
		DestructiveHint: &destructive,
		IdempotentHint:  idempotent,
		OpenWorldHint:   &openWorld,
	}
}
