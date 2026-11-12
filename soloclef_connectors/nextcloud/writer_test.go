package nextcloud_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud"
	"git.danvics.com/danvics/soloclef/soloclef_connectors/nextcloud/nextcloudtest"
)

func writer(c *nextcloudtest.Server) *nextcloud.Writer {
	return nextcloud.NewWriter(c.Account(), c.Transport(), quick)
}

func TestANewFileIsWrittenAndReadBackWithItsID(t *testing.T) {
	c := nextcloudtest.New(t, map[string]node{"/Notes": {Folder: true}})
	made, err := writer(c).Create(context.Background(), "/Notes/", "Week 1 & 2.md", "text/markdown", []byte("# Week"))
	if err != nil {
		t.Fatal(err)
	}
	held, found := c.File("/Notes/Week 1 & 2.md")
	if !found || held.Content != "# Week" {
		t.Fatalf("the server holds %+v, %v", held, found)
	}
	if made.Path != "/Notes/Week 1 & 2.md" || made.FileID != held.FileID || made.FileID == "" {
		t.Errorf("read back as %+v", made)
	}
	// Into the root as well.
	if _, err := writer(c).Create(context.Background(), "", "top.txt", "text/plain", []byte("x")); err != nil {
		t.Errorf("a file in the root: %v", err)
	}
}

func TestAWriteNeverReplacesAFileNorMakesAFolder(t *testing.T) {
	c := nextcloudtest.New(t, map[string]node{"/Notes": {Folder: true}, "/Notes/plan.md": {Content: "the person's own"}})
	_, err := writer(c).Create(context.Background(), "/Notes", "plan.md", "text/markdown", []byte("the model's"))
	if !errors.Is(err, nextcloud.ErrExists) {
		t.Errorf("writing over a file: %v", err)
	}
	if held, _ := c.File("/Notes/plan.md"); held.Content != "the person's own" {
		t.Errorf("the file was replaced: %q", held.Content)
	}
	if _, err := writer(c).Create(context.Background(), "/Invented", "a.md", "text/markdown", []byte("x")); !errors.Is(err, nextcloud.ErrNoFolder) {
		t.Errorf("writing into a folder that is not there: %v", err)
	}
	for _, name := range []string{"", ".", "..", "a/b.md", `a\b.md`, " padded.md", "tab\t.md", strings.Repeat("n", nextcloud.MaxNameBytes+1)} {
		if _, err := writer(c).Create(context.Background(), "/Notes", name, "text/plain", []byte("x")); !errors.Is(err, nextcloud.ErrInvalidPath) {
			t.Errorf("the name %q: %v", name, err)
		}
	}
	if _, err := writer(c).Create(context.Background(), "/Notes/../..", "a.md", "text/plain", []byte("x")); !errors.Is(err, nextcloud.ErrInvalidPath) {
		t.Errorf("a folder that climbs out: %v", err)
	}
}

func TestAFileIsMovedToTheBinAndTheRootNeverIs(t *testing.T) {
	c := nextcloudtest.New(t, map[string]node{"/old.pdf": {Content: "x"}})
	if err := writer(c).Trash(context.Background(), "/old.pdf"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Trashed(), []string{"/old.pdf"}) {
		t.Errorf("trashed %v", c.Trashed())
	}
	if err := writer(c).Trash(context.Background(), "/old.pdf"); !errors.Is(err, nextcloud.ErrNotFound) {
		t.Errorf("binning it again: %v", err)
	}
	asked := len(c.Requests())
	for _, root := range []string{"/", "", "//."} {
		if err := writer(c).Trash(context.Background(), root); !errors.Is(err, nextcloud.ErrInvalidPath) {
			t.Errorf("binning %q: %v", root, err)
		}
	}
	if len(c.Requests()) != asked {
		t.Error("a refused root reached the server")
	}
}

func TestAWriteIsRetriedOnlyWhenNothingWasDone(t *testing.T) {
	c := nextcloudtest.New(t, map[string]node{"/a.pdf": {Content: "x"}, "/b.pdf": {Content: "y"}})
	var gateways atomic.Int32
	c.Before(func(response http.ResponseWriter, request *http.Request) bool {
		if request.Method == http.MethodDelete && gateways.Add(1) == 1 {
			// A gateway's timeout: the delete may have happened behind it.
			response.WriteHeader(http.StatusGatewayTimeout)
			return true
		}
		return false
	})
	if err := writer(c).Trash(context.Background(), "/a.pdf"); !errors.Is(err, nextcloud.ErrUnavailable) || gateways.Load() != 1 {
		t.Errorf("after a 504: %v, %d attempts", err, gateways.Load())
	}

	var busy atomic.Int32
	c.Before(func(response http.ResponseWriter, request *http.Request) bool {
		if request.Method == http.MethodDelete && busy.Add(1) == 1 {
			// Maintenance mode: nothing was done.
			response.WriteHeader(http.StatusServiceUnavailable)
			return true
		}
		return false
	})
	if err := writer(c).Trash(context.Background(), "/b.pdf"); err != nil || busy.Load() != 2 {
		t.Errorf("after a 503: %v, %d attempts", err, busy.Load())
	}
}

func TestAWriteFollowsNoRedirect(t *testing.T) {
	c := nextcloudtest.New(t, map[string]node{"/a.pdf": {Content: "x"}})
	var followed atomic.Bool
	c.Before(func(response http.ResponseWriter, request *http.Request) bool {
		if strings.Contains(request.URL.Path, "elsewhere") {
			followed.Store(true)
			return false
		}
		http.Redirect(response, request, "/remote.php/dav/files/ada/elsewhere", http.StatusTemporaryRedirect)
		return true
	})
	if _, err := writer(c).Create(context.Background(), "/", "n.md", "text/plain", []byte("x")); !errors.Is(err, nextcloud.ErrUnavailable) || followed.Load() {
		t.Errorf("a redirected write: %v, followed=%v", err, followed.Load())
	}
	if err := writer(c).Trash(context.Background(), "/a.pdf"); !errors.Is(err, nextcloud.ErrUnavailable) || followed.Load() {
		t.Errorf("a redirected delete: %v, followed=%v", err, followed.Load())
	}
}
