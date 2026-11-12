package ownmodels

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_agent/model"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

// tinyPNG is a picture the vision package sends as it is.
var tinyPNG = []byte("\x89PNG\r\n\x1a\n a picture")

// visionServer is an OpenAI-compatible endpoint with a vision model: it
// records what it was sent and describes every picture the same way.
type visionServer struct {
	*httptest.Server
	path, auth, org, model, imageURL string
}

func newVisionServer(t *testing.T) *visionServer {
	t.Helper()
	stub := &visionServer{}
	stub.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content []struct {
					Type     string `json:"type"`
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		stub.path, stub.auth, stub.org, stub.model = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("X-Org"), body.Model
		for _, part := range body.Messages[0].Content {
			if part.Type == "image_url" {
				stub.imageURL = part.ImageURL.URL
			}
		}
		// A reasoning model's answer: its thinking is not the description.
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":[{"type":"thinking","text":"hm"},{"type":"text","text":"A chest X-ray."}]}}]}`))
	}))
	t.Cleanup(stub.Close)
	return stub
}

// adasConnections is Ada's connection to server: model 7 on, model 8 off.
func adasConnections(t *testing.T, base string, e Egress) Connections {
	t.Helper()
	seal := newSeal(t)
	key, _ := seal.Seal(ada, "fake-ada-key")
	headers, _ := SealedHeaders(seal, ada, map[string]string{"X-Org": "org-1"})
	return Connections{
		Rows: rows{endpoints: map[string]map[int64]Endpoint{ada: {
			7: {ModelID: "her-vision", Label: "Her vision", BaseURL: base, Enabled: true, SealedKey: key, SealedHeaders: SealedHeadersOf(headers)},
			8: {ModelID: "off-vision", Label: "Off vision", BaseURL: base, Enabled: false, SealedKey: key},
		}}},
		Keys: seal, Egress: e,
		Retry: model.RetryPolicy{Attempts: 1, Backoff: time.Millisecond, MaxBackoff: time.Millisecond},
	}
}

func TestAnOwnVisionModelDescribesAtItsOwnAddressWithItsKeyAndHeaders(t *testing.T) {
	server := newVisionServer(t)
	connections := adasConnections(t, server.URL+"/v1/", egress(&resolver{}, "127.0.0.1"))

	opened, err := connections.OpenVision(context.Background(), ada, 7, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	text, err := opened.Describe(context.Background(), vision.Picture{Name: "xray.png", Data: tinyPNG}, vision.Whole)
	if err != nil || text != "A chest X-ray." {
		t.Fatalf("text %q, err %v", text, err)
	}
	if server.path != "/v1/chat/completions" || server.auth != "Bearer fake-ada-key" || server.org != "org-1" ||
		server.model != "her-vision" || !strings.HasPrefix(server.imageURL, "data:image/png;base64,") {
		t.Fatalf("the endpoint saw %+v", server)
	}

	t.Run("another account's id opens nothing", func(t *testing.T) {
		var unavailable *Unavailable
		if _, err := connections.OpenVision(context.Background(), ben, 7, time.Second); !errors.As(err, &unavailable) || !strings.Contains(unavailable.Message, "removed") {
			t.Fatalf("Ben opened Ada's model: %v", err)
		}
	})
	t.Run("a switched-off model is told, not called", func(t *testing.T) {
		var unavailable *Unavailable
		if _, err := connections.OpenVision(context.Background(), ada, 8, time.Second); !errors.As(err, &unavailable) || !strings.Contains(unavailable.Message, "switched off") {
			t.Fatalf("err = %v", err)
		}
	})
}

// An address that resolves inside the network is refused by the guard,
// and the description fails in the guard's sentence, not as a model that
// is down: the indexer would otherwise try the document again and again.
func TestAnOwnVisionModelInsideTheNetworkIsRefusedAndNotRetried(t *testing.T) {
	server := newVisionServer(t)
	connections := adasConnections(t, server.URL+"/v1", egress(&resolver{}))
	opened, err := connections.OpenVision(context.Background(), ada, 7, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = opened.Describe(context.Background(), vision.Picture{Data: tinyPNG}, vision.Whole)
	var failure *parse.Error
	if !errors.As(err, &failure) || failure.Kind != parse.Rejected || failure.Retryable() ||
		!strings.Contains(failure.Message, "points inside this server's network") {
		t.Fatalf("err = %v", err)
	}
	if server.model != "" {
		t.Fatal("the endpoint was reached")
	}
}

// visionOpener records what it was asked to open.
type visionOpener struct{ asked []string }

func (o *visionOpener) OpenVision(_ context.Context, userID string, id int64, timeout time.Duration) (vision.Endpoint, error) {
	o.asked = append(o.asked, userID+"/"+Choice(id)+"/"+timeout.String())
	return vision.Endpoint{Model: "own"}, nil
}

func TestTheVisionResolverSendsAProxyNameToTheProxyAndAnOwnChoiceToTheAccountsConnection(t *testing.T) {
	own := &visionOpener{}
	resolver := VisionResolver{
		Proxy: vision.Client{BaseURL: "http://proxy.test/v1", Key: "proxy-key", HTTP: &http.Client{}, Timeout: time.Minute},
		Own:   own,
	}
	if err := resolver.Check(); err != nil {
		t.Fatal(err)
	}
	proxied, err := resolver.Open(context.Background(), ada, "openai/own:12")
	if err != nil || proxied.Model != "openai/own:12" || proxied.BaseURL != "http://proxy.test/v1" || proxied.Key != "proxy-key" {
		t.Fatalf("proxy name opened %+v, %v", proxied, err)
	}
	if _, err := resolver.Open(context.Background(), ada, "own:12"); err != nil {
		t.Fatal(err)
	}
	if len(own.asked) != 1 || own.asked[0] != ada+"/own:12/1m0s" {
		t.Fatalf("own got %v", own.asked)
	}
	if _, err := resolver.Open(context.Background(), ada, ""); parse.KindOf(err) != parse.NotConfigured {
		t.Fatalf("no name opened something: %v", err)
	}
	if err := (VisionResolver{Proxy: resolver.Proxy}).Check(); err == nil {
		t.Fatal("a resolver with no opener for own connections passed its check")
	}
}
