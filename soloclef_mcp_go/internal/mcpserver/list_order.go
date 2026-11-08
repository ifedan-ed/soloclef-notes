package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// listInRegistrationOrder puts tools/list back into the order the tools were
// registered in.
//
// The SDK keeps its tools sorted by name, because its pagination cursor is a
// position in that order; the Python server lists them in registration order,
// which is the order a model sees them in and the order the documentation
// describes. Reordering is applied only to a page that is the whole list, so a
// paginated answer — which this server never produces, having 32 tools against
// a page size in the thousands — is left as the SDK built it.
func listInRegistrationOrder(order []string) mcp.Middleware {
	position := make(map[string]int, len(order))
	for index, name := range order {
		position[name] = index
	}
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, request mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, request)
			listed, isList := result.(*mcp.ListToolsResult)
			if err != nil || !isList || listed.NextCursor != "" || len(listed.Tools) != len(order) {
				return result, err
			}
			reordered := make([]*mcp.Tool, len(order))
			for _, tool := range listed.Tools {
				index, known := position[tool.Name]
				if !known {
					return result, err
				}
				reordered[index] = tool
			}
			listed.Tools = reordered
			return listed, nil
		}
	}
}
