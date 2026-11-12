package documents

import (
	"net/http"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentdeletion"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
)

// The four reads sit beside every pattern under /api/v1/documents this tier
// serves. A pair net/http refuses is a process that does not start
// (routing_test.go shows one), so all of them are registered here, on one
// mux, where a refusal is a failed test instead.
//
// None of the four can be mistaken for a document id on the Python tier
// either: each is declared before `/documents/{document_id}` in
// documents_routes.py, and Starlette takes the first route that matches.
func TestTheFourReadsShareAMuxWithEveryDocumentRouteServed(t *testing.T) {
	mux := http.NewServeMux()
	nothing := func(http.ResponseWriter, *http.Request) {}
	for _, pattern := range []string{
		http.MethodDelete + " " + documentdeletion.Path,
		http.MethodPut + " " + folders.DocumentMovePath,
		http.MethodPut + " " + folders.BulkMovePath,
		http.MethodGet + " " + ChunksPath,
		http.MethodGet + " " + VersionsPath,
		http.MethodGet + " " + VersionPath,
		http.MethodPost + " " + RestorePath,
		http.MethodGet + " " + StatusPath,
		http.MethodGet + " " + TypeCountsPath,
		http.MethodGet + " " + WatchedFoldersPath,
		http.MethodGet + " " + TitlesPath,
	} {
		mux.HandleFunc(pattern, nothing)
	}

	for target, want := range map[string]string{
		"GET /api/v1/documents/status":          http.MethodGet + " " + StatusPath,
		"GET /api/v1/documents/type-counts":     http.MethodGet + " " + TypeCountsPath,
		"GET /api/v1/documents/watched-folders": http.MethodGet + " " + WatchedFoldersPath,
		"GET /api/v1/documents/search/titles":   http.MethodGet + " " + TitlesPath,
		// A longer path under one of the three single words is the older routes',
		// with the word as a document id -- as it is on the Python tier.
		"GET /api/v1/documents/status/chunks":   http.MethodGet + " " + ChunksPath,
		"GET /api/v1/documents/status/versions": http.MethodGet + " " + VersionsPath,
		// No route of this tier: the proxy must not send these here.
		"GET /api/v1/documents":        "",
		"GET /api/v1/documents/search": "",
		"GET /api/v1/documents/61":     "",
	} {
		method, path, _ := strings.Cut(target, " ")
		request, err := http.NewRequest(method, path, nil)
		if err != nil {
			t.Fatal(err)
		}
		// A path only another method answers is matched by net/http's own
		// 405, under an empty pattern, which is what "" means here too.
		if _, pattern := mux.Handler(request); pattern != want {
			t.Errorf("%s is matched by %q, want %q", target, pattern, want)
		}
	}
}
