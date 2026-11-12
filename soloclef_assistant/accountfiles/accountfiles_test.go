package accountfiles

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/accountvision"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/fileread"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/selection"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

const (
	ada = "aaaaaaaa-0000-4000-8000-000000000001"
	ben = "bbbbbbbb-0000-4000-8000-000000000002"
	cat = "cccccccc-0000-4000-8000-000000000003"
)

// choices are the accounts' vision choices; broken fails every read.
type choices struct {
	chosen map[string]string
	broken bool
}

func (c choices) VisionChoice(_ context.Context, userID string) (string, error) {
	if c.broken {
		return "", errors.New("the database is down")
	}
	return c.chosen[userID], nil
}

// endpoint is an OpenAI-compatible vision endpoint, standing in for both
// the proxy and an account's own connection: it describes a picture by the
// model and the key it was asked with, and records each.
type endpoint struct {
	*httptest.Server
	mu    sync.Mutex
	asked []string
}

func newEndpoint(t *testing.T) *endpoint {
	t.Helper()
	stub := &endpoint{}
	stub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		said := body.Model + " with " + strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		stub.mu.Lock()
		stub.asked = append(stub.asked, said)
		stub.mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{"content": "described by " + said}}}})
	}))
	t.Cleanup(stub.Close)
	return stub
}

// own opens Ada's own model 42 at the stub with her key; any other is
// gone, as ownmodels says of a removed row.
type own struct{ server *endpoint }

func (o own) OpenVision(_ context.Context, userID string, id int64, timeout time.Duration) (vision.Endpoint, error) {
	if userID != ada || id != 42 {
		return vision.Endpoint{}, &ownmodels.Unavailable{Message: "The model you chose from your own connection has been removed. Choose another model in the model menu."}
	}
	return vision.Endpoint{BaseURL: o.server.URL + "/v1", Key: "ada-key", Model: "her-vision", HTTP: o.server.Client(), Timeout: timeout}, nil
}

// noParsers is a registry the pictures in these tests never reach.
type noParsers struct{}

func (noParsers) Default() selection.Selection { chosen, _ := selection.Of("local"); return chosen }
func (noParsers) Route(selection.Selection, string) (parse.Parser, error) {
	return nil, errors.New("no parser in this test")
}

var picture = fileread.File{Name: "photo.png", Data: []byte("\x89PNG\r\n\x1a\n a picture")}

func reader(t *testing.T, accounts choices, deployment string) (*Reader, *endpoint) {
	t.Helper()
	server := newEndpoint(t)
	files, err := fileread.New(noParsers{}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	resolver := ownmodels.VisionResolver{
		Proxy: vision.Client{BaseURL: server.URL + "/v1", Key: "proxy-key", HTTP: server.Client(), Timeout: 5 * time.Second},
		Own:   own{server},
	}
	built := &Reader{Files: files, Vision: accountvision.Models{Choices: accounts, Default: deployment, Open: resolver}}
	if err := built.Check(); err != nil {
		t.Fatal(err)
	}
	return built, server
}

// Ada chose a model of her own connection, Ben a proxy model, Cat nothing:
// each picture is described by the model its account's rule picks, at the
// address and with the key that model is called with.
func TestAPictureIsDescribedByTheAccountsChoiceElseTheDeployments(t *testing.T) {
	files, server := reader(t, choices{chosen: map[string]string{ada: "own:42", ben: "ben-vision"}}, "site-vision")
	for account, want := range map[string]string{
		ada: "described by her-vision with ada-key",
		ben: "described by ben-vision with proxy-key",
		cat: "described by site-vision with proxy-key",
	} {
		read, err := files.Read(context.Background(), account, "", picture)
		if err != nil || read.Markdown != want || read.By != parse.Vision {
			t.Errorf("%s: %q, %v", account, read.Markdown, err)
		}
	}
	if len(server.asked) != 3 {
		t.Errorf("asked: %v", server.asked)
	}
}

func TestNoModelAnywhereIsSaidSo(t *testing.T) {
	files, server := reader(t, choices{}, "")
	_, err := files.Read(context.Background(), cat, "", picture)
	var refused *fileread.Unreadable
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "no vision model is set") || len(server.asked) != 0 {
		t.Fatalf("error = %v, asked %v", err, server.asked)
	}
}

// A chosen own model that cannot be called is said in its own sentence,
// and neither it nor a choice that could not be read is quietly replaced
// by the deployment's model.
func TestAChoiceThatCannotBeUsedIsNotReplacedByTheDefault(t *testing.T) {
	files, server := reader(t, choices{chosen: map[string]string{ben: "own:7"}}, "site-vision")
	_, err := files.Read(context.Background(), ben, "", picture)
	var refused *fileread.Unreadable
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "has been removed") {
		t.Errorf("a removed own model: %v", err)
	}
	broken, _ := reader(t, choices{broken: true}, "site-vision")
	if _, err := broken.Read(context.Background(), ada, "", picture); err == nil || errors.As(err, &refused) {
		t.Errorf("an unreadable choice: %v", err)
	}
	if slices.ContainsFunc(server.asked, func(said string) bool { return strings.HasPrefix(said, "site-vision") }) || len(server.asked) != 0 {
		t.Errorf("a model was asked: %v", server.asked)
	}
}
