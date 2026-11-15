package publicshare

import "regexp"

// citationMarker is a source chip as the chat and the Studio write it into
// text: "[citation:<where>]" or "【citation:<where>】", with an optional
// zero-width space after the bracket, where <where> is a passage's id or an
// address (soloclef_assistant/cite). The spaces and tabs in front go with
// it, so "x [citation:1]." reads "x." and not "x .".
var citationMarker = regexp.MustCompile(`[ \t]*(?:\[|【)\x{200B}?citation:[^\]】\n]*(?:\]|】)`)

// withoutCitations is shared text with every source marker removed. A
// passage id names a document in the owner's library, which a stranger
// must not be handed, and a chip the public page cannot open is a broken
// control. Done when the page is read rather than when the link is made, so
// links made before this rule are covered too; one pass over one output's
// text per view is cheap.
func withoutCitations(text string) string {
	return citationMarker.ReplaceAllString(text, "")
}
