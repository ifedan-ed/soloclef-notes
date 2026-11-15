package assembly

import (
	"context"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/cite"
)

// citedApp is a connected app's tool whose every answer is a numbered
// source, cited the way a web page is: its result starts with a [n] from
// the turn's registry, and the answer's chip names the app and the tool
// (cite.AppSource), since an app's answer has no address to open.
//
// It is done here, around the tool, and not in mcptools, because numbering
// is the chat's concern and not the MCP client's: the client's job ends at
// the result's text. One label per tool and app means two calls of one
// tool are one source; a chip says where a claim came from, and "that app's
// search" is the honest grain of it (an app's result has no identity of
// its own to number it by).
func citedApp(made tool.Tool, app string) tool.Tool {
	run := made.Run
	label := oneLine(app, 60) + " · " + made.Name
	made.Run = func(ctx context.Context, call tool.Call) (string, error) {
		result, err := run(ctx, call)
		// A failure or a pause (an approval, a question) is passed on as it
		// is: there is nothing to cite, and a pause must reach the loop
		// untouched (agent.IsPause).
		if err != nil || call.Citations == nil || strings.TrimSpace(result) == "" {
			return result, err
		}
		n := call.Citations.Number(cite.AppSource(label))
		return "[" + strconv.Itoa(n) + "] Result from the connected app " + oneLine(app, 60) +
			" (cite what you take from it with [" + strconv.Itoa(n) + "]):\n" + result, nil
	}
	return made
}
