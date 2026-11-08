package markdownmemory

import (
	"strings"
	"time"
)

// legacyMarkers is app/services/memory/document.py's LEGACY_MARKERS: the three
// inline tags an older memory document carried, which the renderer drops.
var legacyMarkers = map[string]bool{"fact": true, "pref": true, "instr": true}

// bullet is a dated memory item.
type bullet struct {
	day  time.Time
	text string
}

// parseBullet is document.py:parse_bullet_line. It tries the canonical form
// first and the legacy one after, and reports nothing for a line that is
// neither — which the document then keeps verbatim.
func parseBullet(line string) (bullet, bool) {
	stripped := pythonStrip(line)
	if !strings.HasPrefix(stripped, "- ") {
		return bullet{}, false
	}
	body := stripped[2:]
	if parsed, ok := parseCanonicalBullet(body); ok {
		return parsed, true
	}
	return parseLegacyBullet(body)
}

// parseCanonicalBullet is `- YYYY-MM-DD: text`. The length test and the slice
// offsets are counted in *characters*, as Python counts them, so a bullet
// carrying multi-byte text before the colon is measured the same way.
func parseCanonicalBullet(body string) (bullet, bool) {
	characters := []rune(body)
	if len(characters) < 13 || string(characters[10:12]) != ": " {
		return bullet{}, false
	}
	day, ok := isoDate(string(characters[:10]))
	if !ok {
		return bullet{}, false
	}
	text := pythonStrip(string(characters[12:]))
	if text == "" {
		return bullet{}, false
	}
	return bullet{day: day, text: text}, true
}

// parseLegacyBullet is `- (YYYY-MM-DD) [fact] text`, whose marker the renderer
// drops while keeping the information — which is what turns
// `- (2026-01-01) [fact] legacy` into `- 2026-01-01: legacy`, and leaves
// `- (2026-01-01) [other] legacy` alone because "other" is not a marker.
func parseLegacyBullet(body string) (bullet, bool) {
	characters := []rune(body)
	if len(characters) < 20 || characters[0] != '(' {
		return bullet{}, false
	}
	if string(characters[11:14]) != ") [" {
		return bullet{}, false
	}
	day, ok := isoDate(string(characters[1:11]))
	if !ok {
		return bullet{}, false
	}
	markerEnd := indexFrom(characters, "] ", 14)
	if markerEnd == -1 {
		return bullet{}, false
	}
	marker := string(characters[14:markerEnd])
	if !legacyMarkers[marker] {
		return bullet{}, false
	}
	text := pythonStrip(string(characters[markerEnd+2:]))
	if text == "" {
		return bullet{}, false
	}
	return bullet{day: day, text: text}, true
}

// indexFrom is str.find(needle, start) counted in characters, which is the
// index parse_legacy_bullet then slices with.
func indexFrom(characters []rune, needle string, start int) int {
	wanted := []rune(needle)
	for index := start; index+len(wanted) <= len(characters); index++ {
		if string(characters[index:index+len(wanted)]) == needle {
			return index
		}
	}
	return -1
}
