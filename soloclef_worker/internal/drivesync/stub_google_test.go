package drivesync

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// stubGoogle stands in for the two Google services a sync talks to: the
// token endpoint and Drive's API. It is written for these tests and keeps
// the rules a sync depends on: a token it did not hand out is refused, a
// file it was not told about is not found, and the change feed is a list
// that a page token is a place in.
type stubGoogle struct {
	*httptest.Server
	mu sync.Mutex

	// files is Drive, by file id.
	files map[string]*stubFile
	// feed is every change there has been, in order. The page token "p3"
	// means "everything from the fourth change on".
	feed []change
	// forgotten are page tokens Drive no longer answers from.
	forgotten map[string]bool
	// pageSize is how many changes one page holds.
	pageSize int

	// refreshes counts renewals; dead are refresh tokens Google refuses.
	refreshes int
	dead      map[string]bool
	// down makes every Drive request answer 503.
	down bool
	// requests is every Drive request's path, in order.
	requests []string
}

// stubFile is one file in the stub's Drive.
type stubFile struct {
	meta remoteFile
	// body is the file's own bytes; exports its exports, by MIME type.
	body    []byte
	exports map[string][]byte
	// status, when set, is answered to a download in place of the bytes.
	status int
}

// accessToken is the token the stub hands out for a renewal and accepts.
func accessTokenFor(refreshToken string) string { return "access-for-" + refreshToken }

func newStubGoogle(t *testing.T) *stubGoogle {
	t.Helper()
	g := &stubGoogle{files: map[string]*stubFile{}, forgotten: map[string]bool{}, dead: map[string]bool{}, pageSize: 2}
	g.Server = httptest.NewServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.Close)
	return g
}

// put adds or replaces a file and records the change in the feed.
func (g *stubGoogle) put(file stubFile) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.files[file.meta.ID] = &file
	meta := file.meta
	g.feed = append(g.feed, change{FileID: meta.ID, File: &meta})
}

// remove deletes a file and records that in the feed.
func (g *stubGoogle) remove(id string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	delete(g.files, id)
	g.feed = append(g.feed, change{FileID: id, Removed: true})
}

// seen is how many Drive requests had this path prefix.
func (g *stubGoogle) seen(prefix string) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	count := 0
	for _, path := range g.requests {
		if strings.HasPrefix(path, prefix) {
			count++
		}
	}
	return count
}

func (g *stubGoogle) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.URL.Path == "/token" {
		g.token(w, r)
		return
	}
	g.requests = append(g.requests, strings.TrimPrefix(r.URL.Path, "/drive/v3"))
	if g.down {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	// Drive knows a caller only by a token Google handed out.
	if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer access-for-") {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/drive/v3")
	switch {
	case path == "/changes/startPageToken":
		_ = json.NewEncoder(w).Encode(map[string]string{"startPageToken": "p" + strconv.Itoa(len(g.feed))})
	case path == "/changes":
		g.changes(w, r)
	case strings.HasPrefix(path, "/files/"):
		g.file(w, r, strings.TrimPrefix(path, "/files/"))
	default:
		w.WriteHeader(http.StatusTeapot)
	}
}

func (g *stubGoogle) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if r.Form.Get("client_id") != "project-1" || r.Form.Get("client_secret") != "project-secret" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	g.refreshes++
	if g.dead[r.Form.Get("refresh_token")] {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error": "invalid_grant"}`)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"access_token": accessTokenFor(r.Form.Get("refresh_token")), "expires_in": 3600})
}

func (g *stubGoogle) changes(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("pageToken")
	from, err := strconv.Atoi(strings.TrimPrefix(token, "p"))
	if err != nil || !strings.HasPrefix(token, "p") || from > len(g.feed) || g.forgotten[token] {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	to := min(from+g.pageSize, len(g.feed))
	reply := map[string]any{"changes": g.feed[from:to]}
	if to < len(g.feed) {
		reply["nextPageToken"] = "p" + strconv.Itoa(to)
	} else {
		reply["newStartPageToken"] = "p" + strconv.Itoa(to)
	}
	_ = json.NewEncoder(w).Encode(reply)
}

func (g *stubGoogle) file(w http.ResponseWriter, r *http.Request, rest string) {
	id, exporting := strings.CutSuffix(rest, "/export")
	file, known := g.files[id]
	if !known {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	switch {
	case exporting:
		body, exported := file.exports[r.URL.Query().Get("mimeType")]
		if !exported {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		g.send(w, file, body)
	case r.URL.Query().Get("alt") == "media":
		if file.body == nil {
			// A Google-native file has no bytes of its own.
			w.WriteHeader(http.StatusForbidden)
			return
		}
		g.send(w, file, file.body)
	default:
		_ = json.NewEncoder(w).Encode(file.meta)
	}
}

func (g *stubGoogle) send(w http.ResponseWriter, file *stubFile, body []byte) {
	if file.status != 0 {
		w.WriteHeader(file.status)
		return
	}
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	_, _ = w.Write(body)
}

// viaJSON is keyword arguments as the queue delivers them.
func viaJSON(t *testing.T, kwargs map[string]any) map[string]any {
	t.Helper()
	wire, err := json.Marshal(kwargs)
	if err != nil {
		t.Fatal(err)
	}
	var read map[string]any
	if err := json.Unmarshal(wire, &read); err != nil {
		t.Fatal(err)
	}
	return read
}
