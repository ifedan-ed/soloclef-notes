package searchconnectors

import "strings"

// This file is app/mcp_security/tool_trust.py's always_asks: the tools that
// keep asking before they run whatever the trusted list says, because each
// fetches an address the model chose and so can carry data out of a
// conversation.

// alwaysAskExact is _ALWAYS_ASK_EXACT.
var alwaysAskExact = map[string]bool{
	"navigate": true, "goto": true, "open_url": true, "web_fetch_exa": true,
	"crawling_exa": true, "tavily_extract": true, "tavily_crawl": true, "tavily_map": true,
}

// alwaysAskParts is _ALWAYS_ASK_PARTS.
var alwaysAskParts = []string{"fetch", "crawl", "navigate", "scrape", "browse"}

// alwaysAsks is always_asks(name): the lowered name is one of the exact
// names, or contains one of the parts. The lowering is Python's, which is why
// it is not strings.ToLower (python_values.go, lowersToASCII).
func alwaysAsks(name string) bool {
	lowered := pyLowerForASCIISearch(name)
	if alwaysAskExact[lowered] {
		return true
	}
	for _, part := range alwaysAskParts {
		if strings.Contains(lowered, part) {
			return true
		}
	}
	return false
}
