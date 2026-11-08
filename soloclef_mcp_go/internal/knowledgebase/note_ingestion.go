package knowledgebase

// Translating a plain note into the document-ingestion envelope.
//
// The REST API ingests free text through the browser-extension document shape
// (title, page content, visit metadata); the backend then chunks and embeds it
// like any saved page. Isolating that mapping lets the tool offer a simple
// title-and-content surface without leaking the envelope's shape.

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mcp_go/internal/pythonjson"
)

var notSlugCharacters = regexp.MustCompile(`[^a-z0-9]+`)

// buildNoteDocument wraps a note in the EXTENSION document payload the create
// endpoint expects.
//
// The ceiling is the same as the Python original's: visit metadata is
// synthesized and the "real page URL" is a stable synthetic link derived from
// the title. A first-class note endpoint would remove both.
func buildNoteDocument(workspaceID int, title, content, sourceURL string, now time.Time) pythonjson.Value {
	capturedAt := isoFormat(now)
	url := sourceURL
	if url == "" {
		url = syntheticURL(title, now)
	}
	metadata := pythonjson.NewObject(
		pythonjson.Field("BrowsingSessionId", pythonjson.Str("surfsense-mcp")),
		pythonjson.Field("VisitedWebPageURL", pythonjson.Str(url)),
		pythonjson.Field("VisitedWebPageTitle", pythonjson.Str(title)),
		pythonjson.Field("VisitedWebPageDateWithTimeInISOString", pythonjson.Str(capturedAt)),
		pythonjson.Field("VisitedWebPageReffererURL", pythonjson.Str("")),
		pythonjson.Field("VisitedWebPageVisitDurationInMilliseconds", pythonjson.Str("0")),
	)
	page := pythonjson.NewObject(
		pythonjson.Field("metadata", metadata),
		pythonjson.Field("pageContent", pythonjson.Str(content)),
	)
	return pythonjson.NewObject(
		pythonjson.Field("document_type", pythonjson.Str("EXTENSION")),
		pythonjson.Field("workspace_id", pythonjson.Num(workspaceID)),
		pythonjson.Field("content", pythonjson.Value{
			Kind: pythonjson.Array, Array: []pythonjson.Value{page},
		}),
	)
}

func syntheticURL(title string, now time.Time) string {
	slug := strings.Trim(notSlugCharacters.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if slug == "" {
		slug = "note"
	}
	return fmt.Sprintf("https://surfsense.local/mcp-note/%s-%s",
		slug, now.UTC().Format("20060102150405"))
}

// isoFormat is datetime.isoformat() for an aware UTC time: microseconds when
// there are any, and "+00:00" rather than "Z".
func isoFormat(moment time.Time) string {
	utc := moment.UTC()
	if utc.Nanosecond() == 0 {
		return utc.Format("2006-01-02T15:04:05") + "+00:00"
	}
	return utc.Format("2006-01-02T15:04:05.000000") + "+00:00"
}
