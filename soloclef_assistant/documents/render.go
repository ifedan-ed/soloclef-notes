package documents

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/cite"
)

// contextHeader tells the model what it is looking at. The warning about
// excerpts matters: without it a model reads three matching chunks as the
// whole document and reports that the document "only says" what they say.
const contextHeader = `<retrieved_context>
These are excerpts from the user's own documents, selected for this query.
Each <document> below is in excerpt view: you are seeing only the passages
that matched, not the whole source. Cite a passage with its [n], written
right after the claim it supports. Do not claim a document says nothing more
than what is shown here.
`

// Render writes hits as the block the model reads, numbering each passage in
// the thread's registry. A passage shown twice in one thread, by this search
// or an earlier one, keeps the number it was first given.
func Render(hits []Hit, registry *citation.Registry) string {
	var b strings.Builder
	b.WriteString(contextHeader)
	for _, hit := range hits {
		b.WriteString(`<document title="`)
		b.WriteString(attribute(hit.Title))
		if source := sourceLabel(hit.DocumentType, hit.Metadata); source != "" {
			b.WriteString(`" source="`)
			b.WriteString(attribute(source))
		}
		b.WriteString("\" view=\"excerpt\">\n")
		for _, chunk := range hit.Chunks {
			n := registry.Number(citation.Source{Kind: cite.Chunk, Ref: strconv.FormatInt(chunk.ID, 10)})
			label := "  [" + strconv.Itoa(n) + "] "
			b.WriteString(label)
			// Continuation lines are indented to the text's column, so a
			// passage with line breaks still reads as one labelled block.
			indent := "\n" + strings.Repeat(" ", len(label))
			b.WriteString(strings.ReplaceAll(strings.TrimSpace(chunk.Content), "\n", indent))
			b.WriteByte('\n')
		}
		b.WriteString("</document>\n")
	}
	b.WriteString("</retrieved_context>")
	return b.String()
}

// attribute makes a value safe inside a double-quoted attribute and one
// line long: a title with a quote or a newline in it must not be able to
// close the tag and start writing instructions.
func attribute(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(value)
}

// typeNames are the document types a reader would not recognise spelled as
// the database spells them.
var typeNames = map[string]string{
	"FILE":          "File",
	"NOTE":          "Note",
	"EXTENSION":     "Saved page",
	"CRAWLED_URL":   "Web",
	"YOUTUBE_VIDEO": "YouTube",
	"ARTIFACT":      "Artifact",
}

// sourceLabel says where a document came from: its kind, and the site when
// its metadata records an address ("Web · example.org").
func sourceLabel(documentType string, metadata []byte) string {
	name, known := typeNames[documentType]
	if !known {
		// GOOGLE_DRIVE_FILE -> "Google Drive".
		trimmed := strings.TrimSuffix(strings.TrimSuffix(documentType, "_FILE"), "_CONNECTOR")
		words := strings.Fields(strings.ReplaceAll(strings.ToLower(trimmed), "_", " "))
		for i, w := range words {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
		name = strings.Join(words, " ")
	}
	if host := sourceHost(metadata); host != "" {
		if name == "" {
			return host
		}
		return name + " · " + host
	}
	return name
}

// sourceHost is the host of the first web address the metadata holds under
// the keys the indexers use. Metadata that is not an object has none.
func sourceHost(metadata []byte) string {
	if len(metadata) == 0 {
		return ""
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(metadata, &fields) != nil {
		return ""
	}
	for _, key := range []string{"url", "source_url", "link", "source"} {
		var value string
		if json.Unmarshal(fields[key], &value) != nil {
			continue
		}
		parsed, err := url.Parse(value)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
			continue
		}
		return strings.TrimPrefix(parsed.Hostname(), "www.")
	}
	return ""
}
