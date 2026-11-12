package academicsearch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
)

// search is one request, checked.
type search struct {
	Query    string
	Results  int
	FullText bool
	// Since and Until are 0 when not given.
	Since, Until int
	// Server is "", "medrxiv" or "biorxiv".
	Server string
}

// body is the request as sent. Pointers tell "not sent" and null (which the
// MCP tool sends for an absent year) from a value.
type body struct {
	Query    *string `json:"query"`
	Results  *int    `json:"max_results"`
	FullText *bool   `json:"full_text"`
	Since    *int    `json:"since"`
	Until    *int    `json:"until"`
	Server   *string `json:"server"`
}

// readSearch checks a body and reports every problem at once. source decides
// which fields mean anything: a year window on an arXiv search is refused
// rather than ignored, because a caller who sent one believes it was applied.
func readSearch(raw []byte, path string) (search, []errorenvelope.Failure) {
	var sent body
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&sent); err != nil {
		return search{}, []errorenvelope.Failure{{Location: []string{"body"}, Message: "The body must be a JSON object of query, max_results, full_text, since, until and server: " + err.Error()}}
	}
	var failures []errorenvelope.Failure
	fail := func(field, message string) {
		failures = append(failures, errorenvelope.Failure{Location: []string{"body", field}, Message: message})
	}
	out := search{Results: defaultResults}
	if sent.Query != nil {
		out.Query = strings.TrimSpace(*sent.Query)
	}
	if out.Query == "" || utf8.RuneCountInString(out.Query) > maxQueryChars {
		fail("query", fmt.Sprintf("A query of 1 to %d characters is required", maxQueryChars))
	}
	if sent.Results != nil {
		out.Results = *sent.Results
		if out.Results < 1 || out.Results > maxResults {
			fail("max_results", fmt.Sprintf("max_results must be from 1 to %d", maxResults))
		}
	}
	year := func(field string, value *int) int {
		if value == nil {
			return 0
		}
		if path != PubMedPath {
			fail(field, "Only a PubMed search takes a year window")
		} else if *value < minYear || *value > maxYear {
			fail(field, fmt.Sprintf("%s must be a year from %d to %d", field, minYear, maxYear))
		}
		return *value
	}
	out.Since, out.Until = year("since", sent.Since), year("until", sent.Until)
	if out.Since > 0 && out.Until > 0 && out.Since > out.Until {
		fail("since", "since must not be later than until")
	}
	if sent.FullText != nil && *sent.FullText {
		if path != PubMedPath {
			fail("full_text", "Only a PubMed search reads full text")
		}
		out.FullText = true
	}
	if sent.Server != nil {
		out.Server = *sent.Server
		switch {
		case path != PreprintPath:
			fail("server", "Only the preprint search takes a server")
		case out.Server != "medrxiv" && out.Server != "biorxiv":
			fail("server", "server must be medrxiv or biorxiv")
		}
	}
	return out, failures
}
