package httptransport_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// bearer sends every request with the caller's API key, as a remote agent
// does.
type bearer struct{ key string }

func (b bearer) RoundTrip(request *http.Request) (*http.Response, error) {
	request = request.Clone(request.Context())
	request.Header.Set("Authorization", "Bearer "+b.key)
	return http.DefaultTransport.RoundTrip(request)
}

// Over the remote transport, soloclef_upload_file's `file_path` names a file
// on the disk of the machine this server runs on, not the caller's. Reading
// it would hand any API key holder the server's own files (its /proc, its
// image, anything mounted) as a document in their account. The tool must
// refuse, and nothing may reach the upload route.
func TestARemoteCallerCannotUploadAFileFromTheServersDisk(t *testing.T) {
	var uploads atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if strings.HasSuffix(request.URL.Path, "/documents/fileupload") {
			uploads.Add(1)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"pending_files": 1, "skipped_duplicates": 0}`))
	}))
	t.Cleanup(backend.Close)

	// A file only the server can see. Synthetic: this repository has a
	// public copy.
	serverOnly := filepath.Join(t.TempDir(), "server-only.txt")
	if err := os.WriteFile(serverOnly, []byte("not the caller's"), 0o600); err != nil {
		t.Fatal(err)
	}

	configuration := keysOnly()
	configuration.BaseURL = backend.URL
	front := served(t, configuration, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0.1.0"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{
			Endpoint:             front.URL + "/mcp",
			HTTPClient:           &http.Client{Transport: bearer{key: "ss_pat_remote"}},
			DisableStandaloneSSE: true,
		}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "soloclef_upload_file",
		Arguments: map[string]any{"file_path": serverOnly},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.IsError {
		t.Errorf("a remote upload of a server path was answered as a success")
	}
	if got := uploads.Load(); got != 0 {
		t.Errorf("the server's file was sent to the upload route %d time(s)", got)
	}
}
