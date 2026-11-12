package deliverables

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/color"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/accountvision"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/vision"
)

// modelProxy is a stand-in for the model proxy: it speaks the two routes this
// package calls, in the OpenAI shape the proxy speaks, and remembers what
// it was sent. It is a stub, not the proxy: what is tested against it is
// this package's half of the conversation.
type modelProxy struct {
	*httptest.Server
	// answer is called for each request with its path and decoded body.
	answer func(path string, body map[string]any) (status int, response string)
	paths  []string
	bodies []map[string]any
	keys   []string
}

func newProxy(t *testing.T, answer func(path string, body map[string]any) (int, string)) *modelProxy {
	t.Helper()
	p := &modelProxy{answer: answer}
	p.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			// The address a provider hands back instead of the bytes.
			status, response := p.answer(r.URL.Path, nil)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, response)
			return
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.Header.Get("Content-Type") != "application/json" {
			http.Error(w, "not JSON", http.StatusBadRequest)
			return
		}
		p.paths, p.bodies, p.keys = append(p.paths, r.URL.Path), append(p.bodies, body), append(p.keys, r.Header.Get("Authorization"))
		status, response := p.answer(r.URL.Path, body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, response)
	}))
	t.Cleanup(p.Close)
	return p
}

func (p *modelProxy) images() ProxyImages {
	return ProxyImages{BaseURL: p.URL + "/v1/", Key: "sk-test", Model: "gpt-image-test", HTTP: p.Client(), Timeout: 5 * time.Second, MaxBytes: 1 << 20}
}

func TestTheImageModelIsAskedThroughTheProxy(t *testing.T) {
	blue := picture(64, 64, color.NRGBA{B: 255, A: 255})
	p := newProxy(t, func(path string, _ map[string]any) (int, string) {
		return 200, `{"created":1,"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(blue) + `","revised_prompt":"A blue square, flat."}]}`
	})
	images := p.images()
	if err := images.Check(); err != nil {
		t.Fatal(err)
	}
	got, err := images.Generate(context.Background(), "a blue square", "")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Data) != string(blue) || got.MimeType != "image/png" || got.Extension != "png" || got.RevisedPrompt != "A blue square, flat." {
		t.Fatalf("image = %s %s %q, %d bytes", got.MimeType, got.Extension, got.RevisedPrompt, len(got.Data))
	}
	// The request: this model, this prompt, exactly one image, and no size
	// when none was asked for. Nothing else is sent.
	if p.paths[0] != "/v1/images/generations" || p.keys[0] != "Bearer sk-test" {
		t.Errorf("asked %s with %q", p.paths[0], p.keys[0])
	}
	if sent, _ := json.Marshal(p.bodies[0]); string(sent) != `{"model":"gpt-image-test","n":1,"prompt":"a blue square"}` {
		t.Errorf("sent %s", sent)
	}
	if _, err := images.Generate(context.Background(), "a blue square", "1536x1024"); err != nil || p.bodies[1]["size"] != "1536x1024" {
		t.Errorf("with a size: %v, %v", p.bodies[1], err)
	}

	// A provider that answers an address: the bytes are fetched at once. A
	// prompt echoed back unchanged is not a revised one.
	p.answer = func(path string, body map[string]any) (int, string) {
		if body == nil {
			return 200, "\xff\xd8\xff\xe0 the bytes of a JPEG"
		}
		return 200, `{"data":[{"url":"` + p.URL + `/files/abc.jpg","revised_prompt":" a fox "}]}`
	}
	got, err = images.Generate(context.Background(), "a fox", "")
	if err != nil || got.MimeType != "image/jpeg" || got.Extension != "jpg" || got.RevisedPrompt != "" || !strings.HasSuffix(string(got.Data), "JPEG") {
		t.Fatalf("by address: %+v, %v", got, err)
	}
}

// Each failure of the image route is the sentence for who can fix it, with
// the proxy's own words kept for the log and out of the sentence.
func TestAFailedImageIsWordedByWhoCanFixIt(t *testing.T) {
	status, response := 0, ""
	p := newProxy(t, func(string, map[string]any) (int, string) { return status, response })
	images := p.images()
	for name, c := range map[string]struct {
		status   int
		response string
		reason   string
	}{
		"refused by the provider": {400, `{"error":{"message":"Your request was rejected by the safety system of provider-x"}}`, imageRequestRefused},
		"rate limited":            {429, `{"error":{"message":"rate limit for key sk-abc"}}`, imageModelBusy},
		"a wrong key":             {401, `{"error":{"message":"invalid key"}}`, imageModelMisset},
		"a model the proxy lacks": {404, `{"error":{"message":"model gpt-image-test not found"}}`, imageModelMisset},
		"the provider down":       {502, `bad gateway`, imageModelUnavailable},
		"no image in the answer":  {200, `{"data":[]}`, imageModelUnusable},
		"not base64":              {200, `{"data":[{"b64_json":"!!!"}]}`, imageModelUnusable},
		"not an image":            {200, `{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString([]byte("<html>")) + `"}]}`, imageModelUnusable},
		"larger than allowed":     {200, `{"data":[{"b64_json":"` + base64.StdEncoding.EncodeToString(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 1<<20)...)) + `"}]}`, imageModelUnusable},
	} {
		status, response = c.status, c.response
		_, err := images.Generate(context.Background(), "x", "")
		var refused *ImageRefusal
		if !errors.As(err, &refused) || refused.Reason != c.reason {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.Contains(refused.Reason, "provider-x") || strings.Contains(refused.Reason, "sk-abc") {
			t.Errorf("%s: the sentence carries the proxy's words: %q", name, refused.Reason)
		}
		// The cause is still there for the log.
		if c.status != 200 && !strings.Contains(errors.Unwrap(err).Error(), "the model proxy answered") {
			t.Errorf("%s: the cause was lost: %v", name, errors.Unwrap(err))
		}
	}

	// A turn that was stopped is a stopped turn, not a failed image model.
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := images.Generate(stopped, "x", ""); !errors.Is(err, context.Canceled) {
		t.Errorf("a stopped turn: %v", err)
	}
	// An image model that never answers is given up on at its limit.
	slow := newProxy(t, func(string, map[string]any) (int, string) {
		time.Sleep(300 * time.Millisecond)
		return 200, `{}`
	})
	hasty := slow.images()
	hasty.Timeout = 30 * time.Millisecond
	var refused *ImageRefusal
	if _, err := hasty.Generate(context.Background(), "x", ""); !errors.As(err, &refused) || refused.Reason != imageModelUnavailable {
		t.Errorf("a slow image model: %v", err)
	}

	// A deployment with none says so, and asks nobody.
	if _, err := (NoImageModel{}).Generate(context.Background(), "x", ""); !errors.As(err, &refused) || refused.Reason != noImageModel {
		t.Errorf("no image model: %v", err)
	}
	if _, err := (NoImageReader{}).Read(context.Background(), "owner-1", "x", nil, ""); !errors.Is(err, ErrNoImageReader) {
		t.Errorf("no reader: %v", err)
	}
	// So does an account with no vision model on a deployment with none.
	none := VisionImageReader{Sight: &sight{err: accountvision.ErrNone}, Timeout: time.Second}
	if _, err := none.Read(context.Background(), "owner-1", "x", nil, ""); !errors.Is(err, ErrNoImageReader) {
		t.Errorf("no reader: %v", err)
	}
}

// sight is the account's vision model, fixed: endpoint, or err. It
// records whom it was opened for.
type sight struct {
	endpoint vision.Endpoint
	err      error
	users    []string
}

func (s *sight) For(_ context.Context, userID string) (vision.Endpoint, error) {
	s.users = append(s.users, userID)
	return s.endpoint, s.err
}

func TestAnImageIsShownToTheReadingModelInline(t *testing.T) {
	p := newProxy(t, func(string, map[string]any) (int, string) {
		return 200, `{"choices":[{"message":{"role":"assistant","content":"{\"items\":[]}"}}]}`
	})
	// The account's vision model, wherever it is called: its address, its
	// key, its name (soloclef_ingest/vision tests the headers and the
	// guard of an own connection).
	seeing := &sight{endpoint: vision.Endpoint{BaseURL: p.URL + "/v1", Key: "sk-test", Model: "vision-test", HTTP: p.Client()}}
	reader := VisionImageReader{Sight: seeing, Timeout: 5 * time.Second}
	if err := reader.Check(); err != nil {
		t.Fatal(err)
	}
	answer, err := reader.Read(context.Background(), "owner-1", "What does it say?", []byte("jpeg-bytes"), "image/jpeg")
	if err != nil || answer != `{"items":[]}` {
		t.Fatalf("answer %q, %v", answer, err)
	}
	if p.paths[0] != "/v1/chat/completions" || p.keys[0] != "Bearer sk-test" {
		t.Errorf("asked %s with %q", p.paths[0], p.keys[0])
	}
	want := `{"messages":[{"content":[{"text":"What does it say?","type":"text"},{"image_url":{"url":"data:image/jpeg;base64,` + base64.StdEncoding.EncodeToString([]byte("jpeg-bytes")) + `"},"type":"image_url"}],"role":"user"}],"model":"vision-test"}`
	if sent, _ := json.Marshal(p.bodies[0]); string(sent) != want {
		t.Errorf("sent %s\nwant %s", sent, want)
	}

	p.answer = func(string, map[string]any) (int, string) { return 500, "boom" }
	if _, err := reader.Read(context.Background(), "owner-1", "x", []byte("j"), "image/jpeg"); err == nil || !strings.Contains(err.Error(), "the vision model answered 500") {
		t.Errorf("a failed reading: %v", err)
	}
	p.answer = func(string, map[string]any) (int, string) { return 200, `{"choices":[]}` }
	if _, err := reader.Read(context.Background(), "owner-1", "x", []byte("j"), "image/jpeg"); err == nil {
		t.Error("an answer with no choice was accepted")
	}
	// A reasoning model's answer is its text parts, not its thinking.
	p.answer = func(string, map[string]any) (int, string) {
		return 200, `{"choices":[{"message":{"content":[{"type":"thinking","text":"hm"},{"type":"text","text":"{\"items\":[]}"}]}}]}`
	}
	if answer, err := reader.Read(context.Background(), "owner-1", "x", []byte("j"), "image/jpeg"); err != nil || answer != `{"items":[]}` {
		t.Errorf("a reasoning model's answer: %q, %v", answer, err)
	}
	if len(seeing.users) != 4 || seeing.users[0] != "owner-1" {
		t.Errorf("opened for %v", seeing.users)
	}
}

// enqueued remembers what was put on the task queue.
// The message a queued video is published as: the task the worker runs,
// the job as a keyword argument, under the task id the row names.
func TestTheVideoMessageIsTheOneTheWorkerClaimsBy(t *testing.T) {
	m := VideoMessage(42, videoTaskID(42))
	if !m.ArgsAsTuple || m.Task != "deliverables.execute_queued" || m.ID != "deliverable-job:42:attempt:1" || len(m.Args) != 0 || len(m.Kwargs) != 1 || m.Kwargs["job_id"] != int64(42) || m.Queue != "" {
		t.Fatalf("message = %+v", m)
	}
}
