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

// cannedReply matches each path from its start, so a tool that put a scope
// segment in front of a route would reach the 404 at the end and its case would
// fail: whose data a call reads is the credential's to say, never a path's.
func cannedReply(request *http.Request) (int, []byte, string) {
	path := request.URL.Path
	switch {
	case path == "/api/v1/academic-search/pubmed":
		return 200, compact(`[{"title": "A synthetic paper",
			"meta": "Doe J. · 2024 · J Synth",
			"url": "https://example.invalid/p/1", "snippet": "Abstract text.",
			"paper": {"doi": "10.0000/synthetic.1", "pmid": "11111111"}}]`),
			"application/json"
	case path == "/api/v1/academic-search/arxiv" || path == "/api/v1/academic-search/medrxiv":
		return 200, compact(`[{"title": "Another synthetic paper",
			"meta": "Roe R. · 2023", "url": "https://example.invalid/p/2",
			"snippet": "More abstract.", "paper": {"arxiv_id": "2301.00001"}}]`),
			"application/json"
	case path == "/api/v1/documents/search-semantic":
		return 200, compact(`{"items": [{"document_id": 7, "title": "Synthetic note",
			"document_type": "NOTE", "rank": 1,
			"chunks": [{"chunk_id": 70, "content": "  matched passage  ", "position": 0, "page": null}]}]}`),
			"application/json"
	case path == "/api/v1/documents" && request.Method == "POST":
		// The create route refuses a field it does not know; so does this.
		var note map[string]any
		if err := json.NewDecoder(request.Body).Decode(&note); err != nil || len(note) != 3 ||
			note["title"] == nil || note["content"] == nil {
			return 422, compact(`{"error": {"message": "the note body is not title, content and source_url"}}`), "application/json"
		}
		if _, sent := note["source_url"]; !sent {
			return 422, compact(`{"error": {"message": "source_url was not sent"}}`), "application/json"
		}
		return 201, compact(`{"id": 8, "title": "Synthetic finding", "document_type": "NOTE"}`), "application/json"
	case path == "/api/v1/documents" && request.Method == "GET":
		return 200, compact(`{"items": [{"id": 7, "title": "Synthetic note",
			"document_type": "EXTENSION", "updated_at": "2026-01-02T03:04:05+00:00"}],
			"total": 3, "page": 0, "has_more": true}`), "application/json"
	case path == "/api/v1/documents/7" && request.Method == "GET":
		if strings.Contains(request.Header.Get("Accept"), "text/markdown") {
			return 200, []byte("---\ntype: note\ntitle: Synthetic note\n---\n\nbody text\n"),
				"text/markdown"
		}
		return 200, compact(`{"id": 7, "title": "Synthetic note",
			"content": "body text"}`), "application/json"
	case path == "/api/v1/documents/7" && request.Method == "DELETE":
		return 200, nil, "application/json"
	case path == "/api/v1/documents/7/save" && request.Method == "POST":
		// A save that pinned a title would be the old two-call update.
		var save map[string]any
		if err := json.NewDecoder(request.Body).Decode(&save); err != nil || len(save) != 1 || save["source_markdown"] == nil {
			return 422, compact(`{"error": {"message": "the save body is not source_markdown alone"}}`), "application/json"
		}
		return 200, compact(`{"status": "saved", "document_id": 7,
			"message": "Document saved and will be reindexed in the background",
			"updated_at": "2026-01-02T03:04:05Z"}`), "application/json"
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
