// Package websearch is the chat's two web search tools, Exa and Tavily.
//
// They are two tools and stay two: the person picks which one a message
// uses, each bills its own provider account, and each is offered only to a person
// who has connected that provider. Nothing here chooses between them or
// falls from one to the other.
//
// # What this does better than the specialists it replaces
//
//   - The model calls the search itself. Each provider used to be a
//     specialist agent wrapped round one tool: a model call to delegate, one
//     to search, one to summarise.
//   - Every result is its own source, cited by its address, so a chip leads
//     to the page a claim came from. A whole search used to be one "run"
//     with one number, whichever of its ten pages a sentence rested on.
//   - A provider's key is looked up when a call is made and held nowhere, so
//     a key removed in settings is gone from the next call.
package websearch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_agent/citation"
	"git.danvics.com/danvics/soloclef/soloclef_agent/tool"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/cite"
)

// Provider is one search service.
type Provider string

const (
	Exa    Provider = "exa"
	Tavily Provider = "tavily"
)

// ToolName is the tool a provider is offered to the model as.
func (p Provider) ToolName() string { return string(p) + "_web_search" }

func (p Provider) label() string {
	if p == Exa {
		return "Exa"
	}
	return "Tavily"
}

// The providers' search endpoints. They are constants, not settings: a key
// for Exa is only ever sent to Exa.
const (
	exaEndpoint    = "https://api.exa.ai/search"
	tavilyEndpoint = "https://api.tavily.com/search"
)

// maxResults is the most results one search returns, and the default.
const maxResults = 10

// Keys finds an account's key for a provider. found is false when the
// account has not connected that provider.
type Keys interface {
	Key(ctx context.Context, userID string, provider Provider) (key string, found bool, err error)
}

// Search is the two tools' shared machinery.
type Search struct {
	Keys Keys
	HTTP *http.Client
	// endpoints is replaced by tests; nil means the real ones.
	endpoints map[Provider]string
}

// Check reports what is missing. Called once at start-up.
func (s Search) Check() error {
	switch {
	case s.Keys == nil:
		return errors.New("websearch: Keys is not set")
	case s.HTTP == nil:
		return errors.New("websearch: the HTTP client is not set")
	}
	return nil
}

// Connected reports whether the account has a key for provider, which is
// whether its tool is offered this turn.
func (s Search) Connected(ctx context.Context, userID string, provider Provider) (bool, error) {
	_, found, err := s.Keys.Key(ctx, userID, provider)
	return found, err
}

var parameters = json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","minLength":1,"maxLength":400,"description":"What to search the web for, in plain words."},"max_results":{"type":"integer","minimum":1,"maximum":10,"description":"How many results to return. Default 10."}},"required":["query"]}`)

// Tool is provider's search for one account. It asks for no approval: it
// sends the provider a query and nothing of the user's documents.
func (s Search) Tool(userID string, provider Provider) tool.Tool {
	return tool.Tool{
		Name: provider.ToolName(),
		Description: "Search the web with " + provider.label() + ". Returns up to 10 results, each labelled [n]: title, address, a short matching passage, and the site and date when known. " +
			"Cite a result by writing its [n] right after the claim it supports.",
		Parameters: parameters,
		Approval:   tool.Auto,
		Parallel:   true,
		Run: func(ctx context.Context, call tool.Call) (string, error) {
			var args struct {
				Query      string `json:"query"`
				MaxResults *int   `json:"max_results"`
			}
			if err := json.Unmarshal(call.Args, &args); err != nil {
				return "", fmt.Errorf("the arguments are not readable: %w", err)
			}
			query := strings.TrimSpace(args.Query)
			if query == "" || len([]rune(query)) > 400 {
				return "", errors.New("the query must be 1 to 400 characters")
			}
			limit := maxResults
			if args.MaxResults != nil {
				limit = min(max(*args.MaxResults, 1), maxResults)
			}
			// Read now, not when the turn was built: a turn can pause for
			// an approval for hours, and the key may have been removed.
			key, found, err := s.Keys.Key(ctx, userID, provider)
			if err != nil {
				return "", fmt.Errorf("the user's %s key could not be read", provider.label())
			}
			if !found {
				return "", fmt.Errorf("%s is not connected any more", provider.label())
			}
			results, err := s.search(ctx, provider, key, query, limit)
			if err != nil {
				return "", err
			}
			if len(results) == 0 {
				return fmt.Sprintf("%s found nothing for %q.", provider.label(), query), nil
			}
			return Render(results, call.Citations), nil
		},
	}
}

// Result is one page a search found.
type Result struct {
	Title   string
	URL     string
	Snippet string
	// Meta is the site, author and date when known, already joined.
	Meta string
}

// Render writes results as the model reads them, numbering each page in the
// thread's registry by its address.
func Render(results []Result, registry *citation.Registry) string {
	var b strings.Builder
	for i, r := range results {
		if i > 0 {
			b.WriteString("\n\n")
		}
		n := registry.Number(citation.Source{Kind: cite.Web, Ref: r.URL})
		b.WriteString("[" + strconv.Itoa(n) + "] ")
		b.WriteString(oneLine(r.Title))
		b.WriteString("\n" + r.URL)
		if r.Meta != "" {
			b.WriteString("\n" + oneLine(r.Meta))
		}
		if r.Snippet != "" {
			b.WriteString("\n" + oneLine(r.Snippet))
		}
	}
	return b.String()
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func (s Search) endpoint(p Provider) string {
	if address, replaced := s.endpoints[p]; replaced {
		return address
	}
	if p == Exa {
		return exaEndpoint
	}
	return tavilyEndpoint
}

// search sends one request. There is no retry: a search the provider
// refused is reported to the model, which can try again or say so, and a
// retry loop here would spend the account's quota without anyone deciding
// to.
func (s Search) search(ctx context.Context, provider Provider, key, query string, limit int) ([]Result, error) {
	var payload any
	header := http.Header{"Content-Type": {"application/json"}}
	switch provider {
	case Exa:
		header.Set("x-api-key", key)
		payload = map[string]any{
			"query": query, "type": "auto", "numResults": limit,
			"contents": map[string]any{"highlights": map[string]any{"maxCharacters": 400, "query": query}},
		}
	case Tavily:
		header.Set("Authorization", "Bearer "+key)
		payload = map[string]any{
			"query": query, "search_depth": "basic", "max_results": limit, "topic": "general",
			"include_answer": false, "include_raw_content": false, "include_images": false, "include_favicon": false,
		}
	default:
		return nil, fmt.Errorf("websearch: unknown provider %q", provider)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint(provider), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header = header
	response, err := s.HTTP.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// The cause can carry the address with a key in its query; the
		// model and the page get a sentence.
		return nil, fmt.Errorf("%s could not be reached just now", provider.label())
	}
	defer response.Body.Close()
	switch status := response.StatusCode; {
	case status == http.StatusOK:
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return nil, &refusal{fmt.Sprintf("%s rejected the user's key; it needs to be re-entered in their connections", provider.label()), ErrKeyRejected}
	case status == http.StatusPaymentRequired || status == http.StatusTooManyRequests || status == 432 || status == 433:
		return nil, &refusal{fmt.Sprintf("the user's %s account is out of quota or rate limited", provider.label()), ErrQuota}
	default:
		return nil, fmt.Errorf("%s is busy just now (status %d)", provider.label(), status)
	}
	// A search answer is a few kilobytes; a megabyte is a wrong answer.
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%s's answer could not be read", provider.label())
	}
	return parse(provider, raw, limit)
}

func parse(provider Provider, raw []byte, limit int) ([]Result, error) {
	var answer struct {
		Results []struct {
			URL   string `json:"url"`
			Title string `json:"title"`
			// Exa
			Highlights    []string `json:"highlights"`
			Summary       string   `json:"summary"`
			Text          string   `json:"text"`
			Author        string   `json:"author"`
			PublishedDate string   `json:"publishedDate"`
			// Tavily
			Content     string `json:"content"`
			PublishedOn string `json:"published_date"`
		} `json:"results"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		return nil, fmt.Errorf("%s's answer is not readable", provider.label())
	}
	out := make([]Result, 0, min(limit, len(answer.Results)))
	for _, r := range answer.Results {
		if len(out) == limit {
			break
		}
		parsed, err := url.Parse(r.URL)
		// Only a web address can be cited and opened; anything else a
		// provider returns is dropped.
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
			continue
		}
		snippet := strings.Join(r.Highlights, " ")
		for _, alternative := range []string{r.Summary, r.Text, r.Content} {
			if snippet == "" {
				snippet = alternative
			}
		}
		date := r.PublishedDate
		if date == "" {
			date = r.PublishedOn
		}
		if len(date) > 10 {
			date = date[:10]
		}
		var meta []string
		for _, part := range []string{strings.TrimPrefix(parsed.Hostname(), "www."), r.Author, date} {
			if part = strings.TrimSpace(part); part != "" {
				meta = append(meta, part)
			}
		}
		title := strings.TrimSpace(r.Title)
		if title == "" {
			title = r.URL
		}
		out = append(out, Result{Title: title, URL: r.URL, Snippet: clip(snippet, 600), Meta: strings.Join(meta, " · ")})
	}
	return out, nil
}

// clip cuts s to at most n runes, on a rune boundary.
func clip(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
