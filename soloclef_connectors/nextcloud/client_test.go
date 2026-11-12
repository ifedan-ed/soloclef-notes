package nextcloud_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud/nextcloudtest"
)

// quick is timings short enough for a test to wait out.
var quick = nextcloud.Timing{Listing: 5 * time.Second, Stall: 2 * time.Second, MaxRetryWait: 20 * time.Millisecond}

type node = nextcloudtest.Node

func library() map[string]node {
	return map[string]node{
		"/Plans":                  {Folder: true},
		"/Plans/2026":             {Folder: true},
		"/Plans/2026/Q1 & Q2.pdf": {Content: "q1q2"},
		"/Plans/zeta.pdf":         {Content: "zeta!"},
		"/Plans/Alpha.pdf":        {Content: "alpha", ETag: "abc123", FileID: "4711"},
		"/Plans/what? #1 50%.md":  {Content: "odd"},
		"/Plans/naïve résumé.pdf": {Content: "accents"},
		"/Plans/unsized.pdf":      {Content: "sixsix", NoSize: true},
		"/loose.pdf":              {Content: "loose"},
	}
}

func client(c *nextcloudtest.Server) *nextcloud.Client {
	return nextcloud.NewClient(c.Account(), c.Transport(), quick)
}

func TestAListingIsReadIntoEntries(t *testing.T) {
	c := nextcloudtest.New(t, library())
	entry, err := client(c).Stat(context.Background(), "/Plans/Alpha.pdf")
	if err != nil {
		t.Fatal(err)
	}
	modified := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	size := int64(5)
	want := nextcloud.Entry{Path: "/Plans/Alpha.pdf", Name: "Alpha.pdf", Size: &size, Modified: &modified, ContentType: "application/pdf", ETag: "abc123", FileID: "4711"}
	if entry.Modified == nil || !entry.Modified.Equal(modified) {
		t.Fatalf("modified %v", entry.Modified)
	}
	entry.Modified = &modified
	if !reflect.DeepEqual(entry, want) {
		t.Errorf("entry %+v, want %+v", entry, want)
	}
	// The id is what a person's link to the file is made of.
	if link := nextcloud.FileURL(c.URL()+"/", entry.FileID); link != c.URL()+"/f/4711" {
		t.Errorf("link %q", link)
	}
	if nextcloud.FileURL(c.URL(), "") != "" {
		t.Error("a file with no id was given a link")
	}

	// A folder: no content type, and its size from Nextcloud's own property.
	folder, err := client(c).Stat(context.Background(), "/Plans")
	if err != nil || !folder.IsFolder || folder.ContentType != "" || folder.Size == nil || *folder.Size != 4096 || folder.Name != "Plans" {
		t.Errorf("folder %+v, %v", folder, err)
	}

	// A file whose length the server left out has no size, not a size of 0.
	unsized, err := client(c).Stat(context.Background(), "/Plans/unsized.pdf")
	if err != nil || unsized.Size != nil {
		t.Errorf("an unsized file was read as %+v, %v", unsized, err)
	}

	// Names that need escaping go out escaped and come back as they are.
	for _, odd := range []string{"/Plans/what? #1 50%.md", "/Plans/naïve résumé.pdf", "/Plans/2026/Q1 & Q2.pdf"} {
		if entry, err := client(c).Stat(context.Background(), odd); err != nil || entry.Path != odd {
			t.Errorf("Stat(%q) = %+v, %v", odd, entry, err)
		}
	}
}

func TestAWalkFindsEveryFileBreadthFirstFoldersFirst(t *testing.T) {
	c := nextcloudtest.New(t, library())
	files, cut, err := client(c).Walk(context.Background(), "/Plans", 100)
	if err != nil || cut {
		t.Fatalf("%v, cut=%v", err, cut)
	}
	var paths []string
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	// The picked folder's own files by name without regard to case, then
	// the sub-folder's. /loose.pdf is outside and is not there.
	want := []string{"/Plans/Alpha.pdf", "/Plans/naïve résumé.pdf", "/Plans/unsized.pdf", "/Plans/what? #1 50%.md", "/Plans/zeta.pdf", "/Plans/2026/Q1 & Q2.pdf"}
	if !reflect.DeepEqual(paths, want) {
		t.Errorf("walked\n%v\nwant\n%v", paths, want)
	}
	// One level at a time, never Depth: infinity.
	if asked := c.Requests(); !reflect.DeepEqual(asked, []string{"PROPFIND /Plans", "PROPFIND /Plans/2026"}) {
		t.Errorf("asked %v", asked)
	}

	// A walk that meets its limit stops and says so.
	files, cut, err = client(c).Walk(context.Background(), "/Plans", 2)
	if err != nil || !cut || len(files) != 2 {
		t.Errorf("a capped walk returned %d files, cut=%v, %v", len(files), cut, err)
	}
}

func TestTheRemoteFailuresACallerTellsApart(t *testing.T) {
	c := nextcloudtest.New(t, library())

	if _, err := client(c).Stat(context.Background(), "/Plans/gone.pdf"); !errors.Is(err, nextcloud.ErrNotFound) {
		t.Errorf("a missing file: %v", err)
	}

	wrong := c.Account()
	wrong.AppPassword = "revoked"
	if _, err := nextcloud.NewClient(wrong, c.Transport(), quick).Stat(context.Background(), "/Plans"); !errors.Is(err, nextcloud.ErrAuth) {
		t.Errorf("a refused password: %v", err)
	}

	// The app password goes out under the login name, not the user id.
	for _, user := range c.Logins() {
		if user != nextcloudtest.Login {
			t.Errorf("a request authenticated as %q", user)
		}
	}

	// A reply that is not a listing.
	c.Before(func(response http.ResponseWriter, _ *http.Request) bool {
		response.WriteHeader(http.StatusMultiStatus)
		_, _ = io.WriteString(response, "<html>maintenance</html>")
		return true
	})
	if _, err := client(c).Stat(context.Background(), "/Plans"); !errors.Is(err, nextcloud.ErrUnavailable) {
		t.Errorf("a reply that is not a multistatus: %v", err)
	}

	// A redirect is never followed: the password must not travel.
	var followed atomic.Bool
	c.Before(func(response http.ResponseWriter, request *http.Request) bool {
		if strings.Contains(request.URL.Path, "elsewhere") {
			followed.Store(true)
			return false
		}
		http.Redirect(response, request, "/remote.php/dav/files/ada/elsewhere", http.StatusFound)
		return true
	})
	if _, err := client(c).Stat(context.Background(), "/Plans"); !errors.Is(err, nextcloud.ErrUnavailable) || followed.Load() {
		t.Errorf("a redirect: %v, followed=%v", err, followed.Load())
	}
}

func TestAServerAskingToSlowDownIsObeyedAFewTimes(t *testing.T) {
	c := nextcloudtest.New(t, library())
	var refusals atomic.Int32
	var limit atomic.Int32
	limit.Store(2)
	c.Before(func(response http.ResponseWriter, _ *http.Request) bool {
		if refusals.Add(1) <= limit.Load() {
			response.Header().Set("Retry-After", "600")
			response.WriteHeader(http.StatusTooManyRequests)
			return true
		}
		return false
	})
	reader := client(c)
	var waited []time.Duration
	reader.SetSleep(func(_ context.Context, wait time.Duration) error {
		waited = append(waited, wait)
		return nil
	})
	if _, err := reader.Stat(context.Background(), "/Plans"); err != nil {
		t.Fatalf("two refusals then an answer: %v", err)
	}
	// Retry-After said ten minutes; the cap is what was waited.
	if !reflect.DeepEqual(waited, []time.Duration{quick.MaxRetryWait, quick.MaxRetryWait}) {
		t.Errorf("waited %v", waited)
	}

	// A server that never relents is given up on after three attempts.
	refusals.Store(0)
	limit.Store(100)
	if _, err := reader.Stat(context.Background(), "/Plans"); !errors.Is(err, nextcloud.ErrUnavailable) || refusals.Load() != 3 {
		t.Errorf("after %d attempts: %v", refusals.Load(), err)
	}
}

func TestADownloadThatGoesQuietIsAbandoned(t *testing.T) {
	c := nextcloudtest.New(t, library())
	release := make(chan struct{})
	defer close(release)
	c.Before(func(response http.ResponseWriter, request *http.Request) bool {
		if request.Method != http.MethodGet {
			return false
		}
		// Half a file, then silence.
		_, _ = io.WriteString(response, "half")
		response.(http.Flusher).Flush()
		select {
		case <-release:
		case <-request.Context().Done():
		}
		return true
	})
	timing := quick
	timing.Stall = 100 * time.Millisecond
	body, err := nextcloud.NewClient(c.Account(), c.Transport(), timing).Open(context.Background(), "/loose.pdf")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	started := time.Now()
	read, err := io.ReadAll(body)
	if !errors.Is(err, nextcloud.ErrUnavailable) || !strings.Contains(err.Error(), "sent nothing") {
		t.Fatalf("read %q, %v", read, err)
	}
	if took := time.Since(started); took > 3*time.Second {
		t.Errorf("gave up after %s, not the stall time", took)
	}
}

func TestAConnectNeedsEveryTimeStated(t *testing.T) {
	if _, err := nextcloud.NewConnect(http.DefaultTransport, nextcloud.Timing{Listing: time.Second, Stall: time.Second}); err == nil {
		t.Error("a connect with no retry cap was built")
	}
	if _, err := nextcloud.NewConnect(nil, quick); err == nil {
		t.Error("a connect with no transport was built")
	}
	connect, err := nextcloud.NewConnect(http.DefaultTransport, quick)
	if err != nil || connect(nextcloud.Account{}) == nil {
		t.Errorf("%v", err)
	}
}

func shelves() map[string]node {
	return map[string]node{
		"/Plans":                 {Folder: true},
		"/Plans/Budget.pdf":      {Content: "budget", ETag: "eb"},
		"/Plans/archive":         {Folder: true},
		"/Plans/archive/a&b.pdf": {Content: "old"},
		"/Zeta":                  {Folder: true},
		"/alpha.pdf":             {Content: "alpha", NoSize: true},
		"/plan for 100%.pdf":     {Content: "percent"},
	}
}

// The client lists one level, folders first, and searches the whole
// account by name; the search leaves out the scope the server returns
// with its matches, and imposes an order the server does not promise.
func TestTheClientListsAndSearches(t *testing.T) {
	c := nextcloudtest.New(t, shelves())
	reader := client(c)
	names := func(entries []nextcloud.Entry) []string {
		listed := make([]string, len(entries))
		for index, entry := range entries {
			listed[index] = entry.Path
		}
		return listed
	}

	listed, err := reader.List(t.Context(), "/Plans")
	if err != nil || !reflect.DeepEqual(names(listed), []string{"/Plans/archive", "/Plans/Budget.pdf"}) {
		t.Errorf("listing /Plans gave %v (%v)", names(listed), err)
	}
	if _, err := reader.List(t.Context(), "/Nowhere"); !errors.Is(err, nextcloud.ErrNotFound) {
		t.Errorf("listing a folder that is not there gave %v", err)
	}

	found, err := reader.Search(t.Context(), "PLAN", 50)
	if err != nil || !reflect.DeepEqual(names(found), []string{"/Plans", "/plan for 100%.pdf"}) {
		t.Errorf("searching for PLAN gave %v (%v)", names(found), err)
	}
	// A name with "&" travels through the XML both ways.
	if found, err := reader.Search(t.Context(), "a&b", 50); err != nil || !reflect.DeepEqual(names(found), []string{"/Plans/archive/a&b.pdf"}) {
		t.Errorf("searching for a&b gave %v (%v)", names(found), err)
	}
	searches := c.Searches()
	if !strings.Contains(searches[len(searches)-1], "<d:literal>%a&amp;b%</d:literal>") {
		t.Errorf("the search was sent as %s", searches[len(searches)-1])
	}
	// The pattern's wildcards are dropped from what was typed, and a text
	// of nothing else asks the server nothing.
	asked := len(c.Requests())
	if found, err := reader.Search(t.Context(), " %_% ", 50); err != nil || len(found) != 0 || len(c.Requests()) != asked {
		t.Errorf("searching for wildcards gave %v (%v) in %d requests", names(found), err, len(c.Requests())-asked)
	}
	if found, err := reader.Search(t.Context(), "100%", 50); err != nil || !reflect.DeepEqual(names(found), []string{"/plan for 100%.pdf"}) {
		t.Errorf("searching for 100%% gave %v (%v)", names(found), err)
	}
	if found, err := reader.Search(t.Context(), "pdf", 2); err != nil || len(found) != 2 {
		t.Errorf("a search limited to two gave %v (%v)", names(found), err)
	}
	// The app password goes with a search exactly as with a listing, and a
	// refused one is told apart.
	stranger := c.Account()
	stranger.AppPassword = "wrong"
	if _, err := nextcloud.NewClient(stranger, c.Transport(), quick).Search(t.Context(), "plan", 50); !errors.Is(err, nextcloud.ErrAuth) {
		t.Errorf("a search with a wrong password gave %v", err)
	}
}
