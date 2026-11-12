package deliverables

import (
	"encoding/json"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
)

// toolCall is a call with the given arguments, as the loop would make it.
func toolCall(args string) tool.Call {
	return tool.Call{ID: "call-1", Args: json.RawMessage(args), Citations: citation.NewRegistry()}
}
