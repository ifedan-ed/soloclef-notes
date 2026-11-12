package documentlibrary

import (
	"encoding/json"
	"strings"
	"time"
)

// A document as an Open Knowledge Format concept (OKF v0.1): a YAML
// frontmatter block and the document's Markdown. It is what the MCP
// server's get_document tool shows, asked for with `Accept: text/markdown`.
//
// The frontmatter is written here by hand and not by a YAML library, for
// one reason: every string in it is a title, a description, a tag or an
// address that a person or a web page supplied, and the one spelling that
// is safe for all of them is a double-quoted scalar. A JSON string is a
// YAML double-quoted scalar, so each value is written with the JSON encoder
// and can never be read as a different type, a second key or the end of the
// block, whatever it contains.

// okfTypes are the concept types of the document types, in the words
// Google's sample bundles use.
var okfTypes = map[string]string{
	"EXTENSION": "Web Page", "CRAWLED_URL": "Web Page", "FILE": "File", "NOTE": "Note",
	"YOUTUBE_VIDEO": "YouTube Video", "GOOGLE_DRIVE_FILE": "Google Drive File",
	"LOCAL_FOLDER_FILE": "File", "ARTIFACT": "Artifact",
}

// resourceKeys are the metadata keys that can hold a document's source
// address, most specific first. The keys differ by where a document came
// from; the first that holds an http(s) address is the resource.
var resourceKeys = []string{
	"webViewLink", "web_view_link", "video_url", "url", "URL", "source_url", "sourceUrl",
	"VisitedWebPageURL", "source", "link", "permalink", "web_url", "html_url",
}

// quoted is a string as a YAML double-quoted scalar.
func quoted(text string) string {
	var out strings.Builder
	encoder := json.NewEncoder(&out)
	// A title with "<" in it is a title, not markup to escape.
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(text)
	return strings.TrimSuffix(out.String(), "\n")
}

// text is a metadata value that is a non-blank string, trimmed.
func text(value any) (string, bool) {
	held, isText := value.(string)
	held = strings.TrimSpace(held)
	return held, isText && held != ""
}

// concept renders the document as an OKF concept.
func concept(row DocumentOfOwnerRow) string {
	var metadata map[string]any
	_ = json.Unmarshal(row.DocumentMetadata, &metadata)

	var out strings.Builder
	out.WriteString("---\n")
	kind, known := okfTypes[row.DocumentType]
	if !known {
		kind = "Document"
	}
	out.WriteString("type: " + quoted(kind) + "\n")
	for _, key := range resourceKeys {
		if address, held := text(metadata[key]); held &&
			(strings.HasPrefix(address, "https://") || strings.HasPrefix(address, "http://")) {
			out.WriteString("resource: " + quoted(address) + "\n")
			break
		}
	}
	if title := strings.TrimSpace(row.Title); title != "" {
		out.WriteString("title: " + quoted(title) + "\n")
	}
	if description, held := text(metadata["description"]); held {
		out.WriteString("description: " + quoted(description) + "\n")
	}
	if list, isList := metadata["tags"].([]any); isList {
		var tags []string
		for _, entry := range list {
			if tag, held := text(entry); held {
				tags = append(tags, "- "+quoted(tag)+"\n")
			}
		}
		if len(tags) > 0 {
			out.WriteString("tags:\n" + strings.Join(tags, ""))
		}
	}
	// When the document last changed, which is when it was made if it never
	// has.
	when := row.CreatedAt
	if row.UpdatedAt != nil {
		when = *row.UpdatedAt
	}
	out.WriteString("timestamp: " + quoted(when.UTC().Format(time.RFC3339)) + "\n")
	out.WriteString("---\n\n")
	out.WriteString(strings.Trim(row.Body, "\n"))
	out.WriteString("\n")
	return out.String()
}
