package notebookresearch

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/rollingdeadline"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
)

const (
	maxQueryChars = 500
	// maxSources is how many sources one search may ask at once.
	maxSources = 4
	// keepAliveEvery is how often a quiet search still sends a progress
	// event, so that no proxy closes the stream and the bar keeps moving.
	keepAliveEvery = 15 * time.Second
	// sourceTimeout is how long one source has. arXiv asks callers to wait
	// three seconds between requests and a busy source is retried twice;
	// ninety seconds covers that, and a source slower than it is dropped so
	// the others are not held back.
	sourceTimeout = 90 * time.Second
	// deepPages is how many of a deep search's web results are read, to
	// quote the page. It is the page reader's own limit for one call.
	deepPages = 5

	searchFailed = "The search didn't go through. Try again, or pick another source."
)

// The progress a search reports, as percentages the apps draw a bar from.
const (
	searchingFrom = 5
	searchingTo   = 35
	readingFrom   = 40
	readingTo     = 85
	rankingAt     = 95
)

// searchRequest is one search: a query, where to look, and how hard.
type searchRequest struct {
	Query string
	// Sources is every source asked, in the order the person picked them.
	Sources []string
	Deep    bool
}

// readSearch checks the body. `sources` is several sources searched side by
// side; without it `source` is the one. There is no default source: the
// apps always say where to look, and a guess here would be a search of
// somewhere nobody chose.
func readSearch(raw []byte) (searchRequest, []string, string) {
	var body struct {
		Query   string    `json:"query"`
		Source  *string   `json:"source"`
		Sources *[]string `json:"sources"`
		Mode    *string   `json:"mode"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return searchRequest{}, []string{"body"}, "The body should be a JSON object of the documented shape"
	}
	request := searchRequest{Query: strings.TrimSpace(body.Query)}
	if request.Query == "" || utf8.RuneCountInString(request.Query) > maxQueryChars {
		return searchRequest{}, []string{"body", "query"}, "A query of 1 to 500 characters is required"
	}
	switch {
	case body.Mode == nil || *body.Mode == "fast":
	case *body.Mode == "deep":
		request.Deep = true
	default:
		return searchRequest{}, []string{"body", "mode"}, "The mode should be 'fast' or 'deep'"
	}
	var asked []string
	field := "sources"
	switch {
	case body.Sources != nil:
		asked = *body.Sources
	case body.Source != nil:
		asked, field = []string{*body.Source}, "source"
	}
	seen := map[string]bool{}
	for _, source := range asked {
		if _, known := sourceNames[source]; !known {
			return searchRequest{}, []string{"body", field}, "An unknown source was named"
		}
		if !seen[source] {
			seen[source] = true
			request.Sources = append(request.Sources, source)
		}
	}
	if len(request.Sources) < 1 || len(request.Sources) > maxSources {
		return searchRequest{}, []string{"body", field}, "Name 1 to 4 sources to search"
	}
	return request, nil, ""
}

// outcome is what one source answered.
type outcome struct {
	source string
	rows   []Row
	failed *failure
}

// search streams one search as server-sent events: progress, each source's
// own outcome when several are asked, then the rows and "done"; or one
// "error" event when no source answered.
func (r Routes) search(x *exchange) {
	raw, ok := x.rawBody()
	if !ok {
		return
	}
	request, location, problem := readSearch(raw)
	if problem != "" {
		x.invalid(location, problem)
		return
	}
	if !x.owned() {
		return
	}
	// The deadline is moved with every event, so a search that outlasts the
	// server's write timeout is not cut while its reader keeps reading. If
	// it cannot be moved the route says so rather than stream under the
	// fixed timeout and be cut mid-search.
	steady, err := rollingdeadline.Wrap(x.response, r.Stall)
	if err != nil {
		x.fail("the search stream's write deadline cannot be moved", err)
		return
	}
	header := x.response.Header()
	header.Set("Content-Type", "text/event-stream")
	header.Set("Cache-Control", "no-cache")
	// Tells nginx-style proxies not to buffer the stream.
	header.Set("X-Accel-Buffering", "no")
	x.response.WriteHeader(http.StatusOK)
	flush := http.NewResponseController(steady).Flush

	// Cancelled when the route returns, which is also when the reader has
	// gone: every source still searching stops with it.
	ctx, stop := context.WithCancel(x.ctx.Context())
	defer stop()
	emit := func(event any) bool {
		encoded, err := json.Marshal(event)
		if err != nil {
			return false
		}
		if _, err := steady.Write([]byte("data: " + string(encoded) + "\n\n")); err != nil {
			return false
		}
		return flush() == nil
	}
	r.run(ctx, x.userID, request, emit)
}

// rawBody reads the request's body whole, or answers the 422.
func (x *exchange) rawBody() ([]byte, bool) {
	var raw json.RawMessage
	if !x.body(&raw) {
		return nil, false
	}
	return raw, true
}

type phaseEvent struct {
	Type     string `json:"type"`
	Phase    string `json:"phase"`
	Progress int    `json:"progress"`
}

func phase(name string, progress int) phaseEvent { return phaseEvent{"phase", name, progress} }

// providerEvent is one source of a several-source search starting,
// answering or failing.
type providerEvent struct {
	Type    string  `json:"type"`
	Source  string  `json:"source"`
	Status  string  `json:"status"`
	Count   *int    `json:"count,omitempty"`
	Code    *string `json:"code,omitempty"`
	Message *string `json:"message,omitempty"`
}

type errorEvent struct {
	Type    string `json:"type"`
	Code    string `json:"code"`
	Message string `json:"message"`
	// Source is the source the failure is worded after, when several were
	// asked.
	Source *string `json:"source,omitempty"`
}

// run is the search itself, narrated through emit. emit reports false when
// the reader has gone, and run stops.
func (r Routes) run(ctx context.Context, userID string, request searchRequest, emit func(any) bool) {
	several := len(request.Sources) > 1
	if !emit(phase("searching", searchingFrom)) {
		return
	}
	if several {
		for _, source := range request.Sources {
			if !emit(providerEvent{Type: "provider", Source: source, Status: "searching"}) {
				return
			}
		}
	}

	limit := r.sourceTimeout
	if limit == 0 {
		limit = sourceTimeout
	}
	// Buffered for every source, so a source that answers after the reader
	// has gone does not wait for ever to hand its answer over.
	answers := make(chan outcome, len(request.Sources))
	for _, source := range request.Sources {
		go func() {
			bounded, done := context.WithTimeout(ctx, limit)
			defer done()
			rows, failed := r.find(bounded, userID, source, request.Query, request.Deep)
			answers <- outcome{source, rows, failed}
		}()
	}

	every := r.keepAlive
	if every == 0 {
		every = keepAliveEvery
	}
	tick := time.NewTicker(every)
	defer tick.Stop()
	outcomes := map[string]outcome{}
	progress := searchingFrom
	for len(outcomes) < len(request.Sources) {
		select {
		case <-ctx.Done():
			return
		case answer := <-answers:
			outcomes[answer.source] = answer
			if several && !emit(answer.event()) {
				return
			}
			share := searchingFrom + (searchingTo-searchingFrom)*len(outcomes)/len(request.Sources)
			progress = max(progress, share)
		case <-tick.C:
			progress = min(searchingTo, progress+3)
		}
		if !emit(phase("searching", progress)) {
			return
		}
	}

	// The rows in turn: the first of each source, then the second of each,
	// so that no source buries the others; a page two sources both found
	// is listed once, under the source picked first.
	var groups [][]Row
	for _, source := range request.Sources {
		if outcomes[source].failed == nil {
			groups = append(groups, outcomes[source].rows)
		}
	}
	if len(groups) == 0 {
		// No source answered: the search failed, worded after the first
		// source picked.
		first := outcomes[request.Sources[0]]
		failed := errorEvent{Type: "error", Code: first.failed.Code, Message: first.failed.Message}
		if several {
			failed.Source = &first.source
		}
		emit(failed)
		return
	}
	rows := interleave(groups)

	if request.Deep {
		if !r.quotePages(ctx, request.Query, rows, emit) {
			return
		}
	}
	if !emit(phase("ranking", rankingAt)) {
		return
	}
	if !emit(struct {
		Type    string `json:"type"`
		Results []Row  `json:"results"`
	}{"results", rows}) {
		return
	}
	emit(struct {
		Type string `json:"type"`
	}{"done"})
}

func (o outcome) event() providerEvent {
	if o.failed != nil {
		return providerEvent{Type: "provider", Source: o.source, Status: "error", Code: &o.failed.Code, Message: &o.failed.Message}
	}
	count := len(o.rows)
	return providerEvent{Type: "provider", Source: o.source, Status: "done", Count: &count}
}

// interleave is the groups' rows in turn, each page once.
func interleave(groups [][]Row) []Row {
	rows := []Row{}
	seen := map[string]bool{}
	for index := 0; ; index++ {
		more := false
		for _, group := range groups {
			if index >= len(group) {
				continue
			}
			more = true
			if row := group[index]; !seen[row.ID] {
				seen[row.ID] = true
				rows = append(rows, row)
			}
		}
		if !more {
			return rows
		}
	}
}

// quotePages is the reading half of a deep search: the top web results are
// read, and each one's snippet becomes the passage of its page that speaks
// to the query, in place of the search service's teaser. Papers are left
// alone: their snippet is the abstract, or the full text a deep PubMed
// search already fetched. A page that cannot be read keeps its teaser.
func (r Routes) quotePages(ctx context.Context, query string, rows []Row, emit func(any) bool) bool {
	var targets []int
	for index, row := range rows {
		if _, web := webProviders[row.Source]; web && len(targets) < deepPages {
			targets = append(targets, index)
		}
	}
	if len(targets) == 0 {
		return true
	}
	if !emit(phase("reading", readingFrom)) {
		return false
	}
	addresses := make([]string, len(targets))
	for i, index := range targets {
		addresses[i] = rows[index].URL
	}
	// One call: the reader fetches them side by side under its own limits.
	// A PDF among them keeps its teaser: quoting one sentence is not worth
	// a parse of the whole file in the middle of a search.
	for i, page := range r.Pages.ReadWebPages(ctx, addresses) {
		if page.Status == pagereader.Read {
			rows[targets[i]].Snippet = excerpt(page.Text, query, snippetChars)
		}
	}
	return ctx.Err() == nil && emit(phase("reading", readingTo))
}
