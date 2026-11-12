package nextcloudimport

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func shelves() map[string]node {
	return map[string]node{
		"/Plans":                 {folder: true},
		"/Plans/Budget.pdf":      {content: "budget", etag: "eb"},
		"/Plans/archive":         {folder: true},
		"/Plans/archive/a&b.pdf": {content: "old"},
		"/Zeta":                  {folder: true},
		"/alpha.pdf":             {content: "alpha", noSize: true},
		"/plan for 100%.pdf":     {content: "percent"},
	}
}

func (d *desk) get(path string) answer {
	d.t.Helper()
	return d.ask(http.MethodGet, path, "")
}

// listing is what browse and search answer.
type listing struct {
	Path    *string `json:"path"`
	Query   *string `json:"query"`
	Entries []struct {
		Path        string  `json:"path"`
		Name        string  `json:"name"`
		IsFolder    bool    `json:"is_folder"`
		Size        *int64  `json:"size"`
		Modified    *string `json:"modified"`
		ContentType *string `json:"content_type"`
		ETag        *string `json:"etag"`
	} `json:"entries"`
}

func read(t *testing.T, got answer) listing {
	t.Helper()
	if got.status != http.StatusOK {
		t.Fatalf("answered %d %s", got.status, got.body)
	}
	var listed listing
	if err := json.Unmarshal([]byte(got.body), &listed); err != nil || listed.Entries == nil {
		t.Fatalf("answered %s (%v)", got.body, err)
	}
	return listed
}

func TestBrowsingShowsOneFolder(t *testing.T) {
	d := newDesk(t, shelves())
	// No path is the root, where the picker opens.
	top := read(t, d.get("/api/v1/connectors/12/nextcloud/browse"))
	if *top.Path != "/" || len(top.Entries) != 4 || top.Entries[0].Path != "/Plans" || !top.Entries[0].IsFolder ||
		top.Entries[2].Path != "/alpha.pdf" {
		t.Fatalf("the root is %+v", top)
	}
	// What the server did not say is null, not zero: a folder has no
	// content type, and this file's size was not listed.
	if top.Entries[0].ContentType != nil || top.Entries[2].Size != nil || top.Entries[2].ETag == nil || top.Entries[2].Modified == nil {
		t.Errorf("the root's entries are %+v", top.Entries)
	}
	inner := read(t, d.get("/api/v1/connectors/12/nextcloud/browse?path="+url.QueryEscape("Plans//archive/")))
	if *inner.Path != "/Plans/archive" || len(inner.Entries) != 1 || inner.Entries[0].Name != "a&b.pdf" || *inner.Entries[0].Size != 3 {
		t.Errorf("a folder asked for untidily is %+v", inner)
	}
	// An empty folder is a list, not null.
	if got := d.get("/api/v1/connectors/12/nextcloud/browse?path=/Zeta"); !strings.Contains(got.body, `"entries":[]`) {
		t.Errorf("an empty folder answered %s", got.body)
	}

	for name, tc := range map[string]struct {
		path   string
		status int
		code   string
	}{
		"a path that climbs out":         {"/api/v1/connectors/12/nextcloud/browse?path=" + url.QueryEscape("/Plans/../../etc"), http.StatusBadRequest, ""},
		"a folder that is not there":     {"/api/v1/connectors/12/nextcloud/browse?path=/Nowhere", http.StatusNotFound, ""},
		"somebody else's connection":     {"/api/v1/connectors/13/nextcloud/browse", http.StatusNotFound, ""},
		"a connection nobody has":        {"/api/v1/connectors/999/nextcloud/browse", http.StatusNotFound, ""},
		"a connection to another server": {"/api/v1/connectors/14/nextcloud/browse", http.StatusBadRequest, "nextcloud_reconnect"},
		"an id that is not a number":     {"/api/v1/connectors/twelve/nextcloud/browse", http.StatusUnprocessableEntity, ""},
	} {
		got := d.get(tc.path)
		if got.status != tc.status || (tc.code != "" && got.code != tc.code) {
			t.Errorf("%s: answered %d %s", name, got.status, got.body)
		}
	}
	// Somebody else's connection and no connection are one answer, and
	// neither reached Nextcloud with anybody's password.
	theirs, nobodys := d.get("/api/v1/connectors/13/nextcloud/browse"), d.get("/api/v1/connectors/999/nextcloud/browse")
	if theirs.message != nobodys.message {
		t.Errorf("another account's connection is told apart: %q and %q", theirs.message, nobodys.message)
	}
	// A password Nextcloud refuses is a 400 with its code, never a 401,
	// which would sign the person out of this app.
	d.cloud.before = func(response http.ResponseWriter, _ *http.Request) bool {
		response.WriteHeader(http.StatusUnauthorized)
		return true
	}
	if got := d.get("/api/v1/connectors/12/nextcloud/browse"); got.status != http.StatusBadRequest || got.code != "nextcloud_auth" {
		t.Errorf("a refused app password answered %d %s", got.status, got.body)
	}
}

func TestSearchingFindsByName(t *testing.T) {
	d := newDesk(t, shelves())
	found := read(t, d.get("/api/v1/connectors/12/nextcloud/search?q=plan"))
	if *found.Query != "plan" || len(found.Entries) != 2 || found.Entries[0].Path != "/Plans" || found.Entries[1].Path != "/plan for 100%.pdf" {
		t.Errorf("searching for plan answered %+v", found)
	}
	if none := read(t, d.get("/api/v1/connectors/12/nextcloud/search?q=zzzz")); len(none.Entries) != 0 {
		t.Errorf("a search that finds nothing answered %+v", none)
	}
	asked := len(d.cloud.requests())
	for name, path := range map[string]string{
		"no text":           "/api/v1/connectors/12/nextcloud/search",
		"one letter":        "/api/v1/connectors/12/nextcloud/search?q=p",
		"a very long text":  "/api/v1/connectors/12/nextcloud/search?q=" + strings.Repeat("p", 201),
		"another account's": "/api/v1/connectors/13/nextcloud/search?q=plan",
	} {
		got := d.get(path)
		want := http.StatusUnprocessableEntity
		if name == "another account's" {
			want = http.StatusNotFound
		}
		if got.status != want {
			t.Errorf("%s: answered %d %s", name, got.status, got.body)
		}
	}
	if len(d.cloud.requests()) != asked {
		t.Errorf("refused searches reached Nextcloud: %v", d.cloud.requests()[asked:])
	}
}
