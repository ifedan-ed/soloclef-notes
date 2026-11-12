package documents

import (
	"net/http"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/documentdeletion"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/folders"
)

// Every document route cmd/api mounts sits on one mux beside the document
// routes of the other packages. A pair net/http refuses is a process that
// does not start (it panics while the routes are mounted), so the pairs are
// registered here, where a refusal is a failed test instead. The citation
// route is among them: it moved to /chunks/{chunk_id}/document because its
// Python path could not be (documents.go).
func TestTheDocumentRoutesShareOneMux(t *testing.T) {
	mux := http.NewServeMux()
	nothing := func(http.ResponseWriter, *http.Request) {}
	for _, pattern := range []string{
		http.MethodDelete + " " + documentdeletion.Path,
		http.MethodPut + " " + folders.DocumentMovePath,
		http.MethodPut + " " + folders.BulkMovePath,
		http.MethodGet + " " + ByChunkPath,
		http.MethodGet + " " + ChunksPath,
		http.MethodGet + " " + VersionsPath,
		http.MethodGet + " " + VersionPath,
		http.MethodPost + " " + RestorePath,
	} {
		mux.HandleFunc(pattern, nothing)
	}

	// And each request goes to the pattern that is its own.
	for target, want := range map[string]string{
		"GET /api/v1/chunks/704/document":              http.MethodGet + " " + ByChunkPath,
		"GET /api/v1/documents/61/chunks":              http.MethodGet + " " + ChunksPath,
		"GET /api/v1/documents/61/versions":            http.MethodGet + " " + VersionsPath,
		"GET /api/v1/documents/61/versions/2":          http.MethodGet + " " + VersionPath,
		"POST /api/v1/documents/61/versions/2/restore": http.MethodPost + " " + RestorePath,
		// The old citation path is nobody's: the clients ask the new one.
		"GET /api/v1/documents/by-chunk/704":  "",
		"GET /api/v1/documents/61":            "",
		"GET /api/v1/documents/search/titles": "",
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
