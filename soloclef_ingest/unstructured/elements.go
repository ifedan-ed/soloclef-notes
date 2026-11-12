package unstructured

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/htmlmarkdown"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// element is one entry of the reply, with the fields this renderer reads.
type element struct {
	Type     string `json:"type"`
	Text     string `json:"text"`
	Metadata struct {
		// PageNumber is present for paged formats (PDF, DOCX, PPT, XLSX)
		// and absent, so 0, for the rest.
		PageNumber int `json:"page_number"`
		// CategoryDepth is a title's depth among titles: 0 is the top.
		CategoryDepth int `json:"category_depth"`
		// TextAsHTML is a table as HTML; Text holds the same cells run
		// together with no structure.
		TextAsHTML string `json:"text_as_html"`
	} `json:"metadata"`
}

// render reads the element array from the stream, one element at a time,
// and writes each into the page it belongs to. The reply for a long PDF is
// many megabytes of JSON, most of it metadata this never keeps.
func render(body io.Reader, limit int64) (parse.Document, error) {
	limited := &io.LimitedReader{R: body, N: limit + 1}
	decoder := json.NewDecoder(limited)
	bad := func(err error) (parse.Document, error) {
		if limited.N <= 0 {
			return parse.Document{}, &parse.Error{
				Kind: parse.BadResponse, Parser: parse.Unstructured,
				Message: fmt.Sprintf("the reply is larger than the %d bytes allowed back", limit),
			}
		}
		return parse.Document{}, &parse.Error{Kind: parse.BadResponse, Parser: parse.Unstructured, Message: "the reply is not the element array expected", Err: err}
	}

	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		if err == nil {
			err = fmt.Errorf("expected an array, found %v", token)
		}
		return bad(err)
	}

	builder := parse.Builder{Separator: "\n\n"}
	var page strings.Builder
	pageNumber := 0 // 0 until an element names a page
	inList := false
	flush := func() {
		if pageNumber != 0 {
			builder.Page(pageNumber, page.String())
			page.Reset()
		}
	}

	for decoder.More() {
		var next element
		if err := decoder.Decode(&next); err != nil {
			return bad(err)
		}
		if number := next.Metadata.PageNumber; number != 0 && number != pageNumber {
			// What was gathered before any page was named (rare: a cover
			// element without a number) stays with the first page.
			flush()
			pageNumber = number
			inList = false
		}
		block, isListItem, err := markdownOf(next)
		if err != nil {
			return parse.Document{}, err
		}
		if block == "" {
			continue
		}
		if page.Len() > 0 {
			if isListItem && inList {
				// Items of one list sit on consecutive lines.
				page.WriteByte('\n')
			} else {
				page.WriteString("\n\n")
			}
		}
		page.WriteString(block)
		inList = isListItem
	}
	if _, err := decoder.Token(); err != nil {
		return bad(err)
	}

	if pageNumber == 0 {
		// No element named a page: a format without pages.
		return parse.Unpaged(parse.Unstructured, page.String())
	}
	flush()
	return builder.Document(parse.Unstructured)
}

// markdownOf renders one element. The second result says it is a list item.
func markdownOf(e element) (block string, isListItem bool, err error) {
	text := strings.TrimSpace(e.Text)
	switch e.Type {
	case "Header", "Footer", "PageNumber", "PageBreak":
		// Page furniture: the same running title on every page would be
		// indexed once per page and match every search for it. Page breaks
		// carry no text; pages come from each element's page_number.
		return "", false, nil
	case "Title":
		if text == "" {
			return "", false, nil
		}
		// Depth 0 is the top heading. Markdown has six levels.
		level := min(e.Metadata.CategoryDepth, 5) + 1
		return strings.Repeat("#", level) + " " + text, false, nil
	case "ListItem":
		if text == "" {
			return "", false, nil
		}
		return "- " + text, true, nil
	case "Table":
		if e.Metadata.TextAsHTML == "" {
			// Only some strategies infer a table's structure. Without it
			// the cells' text is all there is.
			return text, false, nil
		}
		// The HTML is the table; Text is its cells with the structure gone.
		table, err := htmlmarkdown.Fragment(e.Metadata.TextAsHTML)
		if err != nil {
			return "", false, &parse.Error{Kind: parse.BadResponse, Parser: parse.Unstructured, Message: "a table's HTML could not be converted", Err: err}
		}
		return table, false, nil
	case "Formula":
		if text == "" {
			return "", false, nil
		}
		return "$$\n" + text + "\n$$", false, nil
	case "CodeSnippet":
		if text == "" {
			return "", false, nil
		}
		return "```\n" + text + "\n```", false, nil
	default:
		// NarrativeText, UncategorizedText, FigureCaption, Address,
		// EmailAddress, Image (its text is what was read off the picture),
		// and any type Unstructured adds later: its text, as a paragraph.
		return text, false, nil
	}
}
