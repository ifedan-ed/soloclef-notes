package mcpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeBackend answers exactly as the stand-in backend the two servers were
// compared against answered, so the captured results can be replayed here.
//
// Synthetic throughout, with example.invalid addresses: this repository has a
// public copy.
func fakeBackend(t *testing.T) *httptest.Server {
	t.Helper()
	backend := httptest.NewServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			status, body, contentType := cannedReply(request)
			writer.Header().Set("Content-Type", contentType)
			writer.WriteHeader(status)
			_, _ = writer.Write(body)
		}))
	t.Cleanup(backend.Close)
	return backend
}

const workspacesReply = `[
  {"id": 1, "name": "Research", "description": "Synthetic space", "is_owner": true,
   "member_count": 2},
  {"id": 2, "name": "Research Archive", "description": null, "is_owner": false,
   "member_count": 1}
]`

func cannedReply(request *http.Request) (int, []byte, string) {
	path := request.URL.Path
	switch {
	case path == "/api/v1/workspaces":
		return 200, compact(workspacesReply), "application/json"
	case strings.HasSuffix(path, "/academic-search/pubmed"):
		return 200, compact(`[{"title": "A synthetic paper",
			"meta": "Doe J. · 2024 · J Synth",
			"url": "https://example.invalid/p/1", "snippet": "Abstract text.",
			"paper": {"doi": "10.0000/synthetic.1", "pmid": "11111111"}}]`),
			"application/json"
	case strings.Contains(path, "/academic-search/"):
		return 200, compact(`[{"title": "Another synthetic paper",
			"meta": "Roe R. · 2023", "url": "https://example.invalid/p/2",
			"snippet": "More abstract.", "paper": {"arxiv_id": "2301.00001"}}]`),
			"application/json"
	case path == "/api/v1/documents/search-semantic":
		return 200, compact(`{"items": [{"document_id": 7, "title": "Synthetic note",
			"score": 0.4213, "chunks": [{"content": "  matched passage  "}]}]}`),
			"application/json"
	case path == "/api/v1/documents" && request.Method == "GET":
		return 200, compact(`{"items": [{"id": 7, "title": "Synthetic note",
			"document_type": "EXTENSION", "updated_at": "2026-01-02T03:04:05+00:00"}],
			"total": 3, "page": 0, "has_more": true}`), "application/json"
	case path == "/api/v1/documents/7" && request.Method == "GET":
		if strings.Contains(request.Header.Get("Accept"), "text/markdown") {
			return 200, []byte("---\ntype: note\ntitle: Synthetic note\n---\n\nbody text\n"),
				"text/markdown"
		}
		return 200, compact(`{"id": 7, "title": "Synthetic note", "workspace_id": 1,
			"content": "body text"}`), "application/json"
	case strings.HasPrefix(path, "/api/v1/documents/") && request.Method == "DELETE":
		return 200, nil, "application/json"
	case strings.HasSuffix(path, "/save"):
		return 200, compact(`{"ok": true}`), "application/json"
	case strings.Contains(path, "/scrapers/runs/"):
		return 200, compact(`{"id": "run-0001", "capability": "web.crawl",
			"status": "success", "item_count": 1,
			"created_at": "2026-01-02T03:04:05+00:00",
			"output": {"items": [{"url": "https://example.invalid/a"}]}}`),
			"application/json"
	case strings.HasSuffix(path, "/scrapers/runs"):
		return 200, compact(`[{"id": "run-0001", "capability": "web.crawl",
			"status": "success", "item_count": 1,
			"created_at": "2026-01-02T03:04:05+00:00"}]`), "application/json"
	case strings.Contains(path, "/scrapers/"):
		return 200, compact(`{"run_id": "run-0001", "items": [{
			"url": "https://example.invalid/a", "title": "Synthetic page",
			"body": "` + strings.Repeat("x", 1600) + `",
			"html": "<p>duplicate</p>", "score": 12}]}`), "application/json"
	}
	return 404, compact(`{"detail": "no stub for this path"}`), "application/json"
}

// compact removes the indentation this file uses for readability, so the reply
// is the one line the stand-in backend sent.
//
// Line by line rather than through a decode and re-encode: a round trip through
// a Go map would sort the members, and the order a reply arrives in is the order
// a tool's JSON form prints it in. Trimming at line boundaries only, so the
// whitespace inside a value — a passage with its own leading spaces — survives.
func compact(pretty string) []byte {
	var lines []string
	for _, line := range strings.Split(pretty, "\n") {
		lines = append(lines, strings.TrimSpace(line))
	}
	joined := strings.Join(lines, "")
	if !json.Valid([]byte(joined)) {
		panic("canned reply is not valid JSON: " + joined)
	}
	return []byte(joined)
}
