package mcpserver

import (
	"errors"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

// isUnknownTool recognises the SDK's own refusal of a tool it does not hold.
//
// Matched on the code and the wording together: the code alone also covers a
// malformed params object, which the Python server does not answer this way.
func isUnknownTool(err error) bool {
	var refusal *jsonrpc.Error
	if !errors.As(err, &refusal) {
		return false
	}
	return refusal.Code == jsonrpc.CodeInvalidParams &&
		strings.HasPrefix(refusal.Message, "unknown tool ")
}
