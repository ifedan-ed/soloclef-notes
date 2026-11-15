package notebookresearch

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/notebooksources"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
)

const (
	maxImportItems  = 50
	maxTitleChars   = 500
	maxSnippetChars = 4000
	// storedSnippetChars is how much of a snippet the document keeps: it
	// is the text shown until the page is read, not the page.
	storedSnippetChars = 1000

	notPublic = "That address isn't a public web page. Paste an http or https link to a site on the internet."
)

// item is one result to import, checked.
type item struct {
	address *url.URL
	url     string
	title   string
	snippet string
	source  string
	paper   *Paper
}

// readItems checks an import's body, returning the items once each (two
// addresses of one page are one item) or where the body is wrong.
func readItems(raw []byte) ([]item, []string, string) {
	var body struct {
		Items []struct {
			URL     string  `json:"url"`
			Title   string  `json:"title"`
			Snippet *string `json:"snippet"`
			Source  *string `json:"source"`
			Paper   *Paper  `json:"paper"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, []string{"body"}, "The body should be a JSON object of the documented shape"
	}
	if len(body.Items) < 1 || len(body.Items) > maxImportItems {
		return nil, []string{"body", "items"}, "Send 1 to 50 results to import"
	}
	var items []item
	seen := map[string]bool{}
	for index, sent := range body.Items {
		at := func(field string) []string { return []string{"body", "items", strconv.Itoa(index), field} }
		address, text, ok := webAddress(sent.URL)
		if !ok {
			return nil, at("url"), "The address must be an http(s) URL"
		}
		checked := item{address: address, url: text, title: strings.TrimSpace(sent.Title), source: "web"}
		if checked.title == "" || utf8.RuneCountInString(checked.title) > maxTitleChars || strings.ContainsRune(checked.title, 0) {
			return nil, at("title"), "A title of 1 to 500 characters is required"
		}
		if sent.Snippet != nil {
			if utf8.RuneCountInString(*sent.Snippet) > maxSnippetChars {
				return nil, at("snippet"), "The snippet is longer than 4000 characters"
			}
			checked.snippet = strings.ReplaceAll(trim(*sent.Snippet, storedSnippetChars), "\x00", "")
		}
		if sent.Source != nil {
			// "web" is a link nobody searched for: a pasted one.
			if _, known := sourceNames[*sent.Source]; !known && *sent.Source != "web" {
				return nil, at("source"), "An unknown source was named"
			}
			checked.source = *sent.Source
		}
		if sent.Paper != nil {
			paper, bad := sent.Paper.checked()
			if bad != "" {
				return nil, at("paper"), "The paper's " + bad + " is not one"
			}
			checked.paper = &paper
		}
		if key := sameSourceKey(address); !seen[key] {
			seen[key] = true
			items = append(items, checked)
		}
	}
	return items, nil, ""
}

// importResults adds the picked results as sources.
func (r Routes) importResults(x *exchange) {
	raw, ok := x.rawBody()
	if !ok {
		return
	}
	items, location, problem := readItems(raw)
	if problem != "" {
		x.invalid(location, problem)
		return
	}
	var named struct {
		Parser string `json:"parser"`
	}
	if err := json.Unmarshal(raw, &named); err != nil {
		x.invalid([]string{"body", "parser"}, "The parser should be a parser's name")
		return
	}
	parser, ok := r.chosenParser(x, named.Parser)
	if !ok {
		return
	}
	r.add(x, items, parser)
}

// chosenParser checks the parser an import named for the PDFs among its
// addresses. None is nil: a PDF is then read by the local parser
// (StorePDF), the rule a PDF link was given, and not by the deployment's
// default, which is a picker's starting point for uploads. Any other value
// must be one of the four names and configured here, refused by name if
// not, before anything is written.
func (r Routes) chosenParser(x *exchange, named string) (*string, bool) {
	if named == "" {
		return nil, true
	}
	chosen, refused := parserchoice.Choose(r.Parsers, named)
	if refused != nil {
		x.invalid([]string{"body", "parser"}, refused.Message)
		return nil, false
	}
	value := chosen.String()
	return &value, true
}

// readPage adds one pasted web page as a source.
func (r Routes) readPage(x *exchange) {
	var body struct {
		URL    string `json:"url"`
		Parser string `json:"parser"`
	}
	if !x.body(&body) {
		return
	}
	address, text, ok := webAddress(body.URL)
	if !ok {
		x.invalid([]string{"body", "url"}, "The address must be an http(s) URL")
		return
	}
	if plainlyPrivate(address) {
		x.refuse(http.StatusUnprocessableEntity, notPublic)
		return
	}
	parser, ok := r.chosenParser(x, body.Parser)
	if !ok {
		return
	}
	// Titled with its own address until the page says what it is called.
	r.add(x, []item{{address: address, url: text, title: clip(text, maxTitleChars), source: "web"}}, parser)
}

// add links every item to the notebook through the import (Imports.Add)
// and answers the notebook's sources, as both routes do.
func (r Routes) add(x *exchange, items []item, parser *string) {
	_, err := r.imports().Add(x.ctx.Context(), Importing{UserID: x.userID, NotebookID: x.notebookID, RequestID: x.requestID, Parser: parser}, items)
	if errors.Is(err, errNotebookAbsent) {
		x.refuse(http.StatusNotFound, notebookAbsent)
		return
	}
	if err != nil {
		x.fail("could not import the results", err)
		return
	}
	listed, err := notebooksources.Listing(x.ctx.Context(), r.Pool, x.userID, x.notebookID)
	if err != nil {
		x.fail("could not list the notebook's sources", err)
		return
	}
	x.send(http.StatusOK, listed)
}
