package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/image/bmp"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// square is a small PNG: a red square on white.
func square(t *testing.T, side int) []byte {
	t.Helper()
	picture := image.NewRGBA(image.Rect(0, 0, side, side))
	for x := range side {
		for y := range side {
			picture.Set(x, y, color.White)
			if x > side/4 && x < 3*side/4 && y > side/4 && y < 3*side/4 {
				picture.Set(x, y, color.RGBA{R: 255, A: 255})
			}
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, picture); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// seen is what the stub proxy was sent.
type seen struct {
	Authorization string
	Model         string
	Prompt        string
	ImageURL      string
}

// stub is a proxy that answers every description with reply, after
// recording the request.
func stub(t *testing.T, status int, reply string, got *seen) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Method != http.MethodPost {
			t.Errorf("asked %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content []struct {
					Type     string `json:"type"`
					Text     string `json:"text"`
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("the request is not the JSON expected: %v", err)
		}
		if got != nil {
			got.Authorization, got.Model = r.Header.Get("Authorization"), body.Model
			for _, part := range body.Messages[0].Content {
				if part.Type == "text" {
					got.Prompt = part.Text
				} else {
					got.ImageURL = part.ImageURL.URL
				}
			}
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(server.Close)
	return server
}

func client(server *httptest.Server) Client {
	return Client{BaseURL: server.URL + "/v1", Key: "proxy-key", HTTP: server.Client(), Timeout: 5 * time.Second}
}

// endpoint is the stub proxy's model by this name.
func endpoint(t *testing.T, server *httptest.Server, model string) Endpoint {
	t.Helper()
	opened, err := client(server).Endpoint(model)
	if err != nil {
		t.Fatal(err)
	}
	return opened
}

func TestAPictureIsSentAsADataURLAndTheAnswerIsItsDescription(t *testing.T) {
	var got seen
	server := stub(t, 200, `{"choices":[{"message":{"content":"  A red square on white.  "}}]}`, &got)
	picture := square(t, 64)

	text, err := endpoint(t, server, "vision-model").Describe(context.Background(), Picture{Name: "square.png", Data: picture}, Whole)
	if err != nil {
		t.Fatal(err)
	}
	if text != "A red square on white." {
		t.Errorf("description = %q", text)
	}
	if got.Authorization != "Bearer proxy-key" || got.Model != "vision-model" {
		t.Errorf("sent with %q to %q", got.Authorization, got.Model)
	}
	// A small PNG goes as it is, byte for byte.
	if got.ImageURL != "data:image/png;base64,"+base64.StdEncoding.EncodeToString(picture) {
		t.Errorf("the picture was not sent as it is: %.60s", got.ImageURL)
	}
	if !strings.Contains(got.Prompt, "Transcribe any visible text") {
		t.Errorf("a whole picture's prompt asks for its text too: %q", got.Prompt)
	}
}

func TestAnEmbeddedPictureIsNotAskedForItsText(t *testing.T) {
	var got seen
	server := stub(t, 200, `{"choices":[{"message":{"content":"A chart."}}]}`, &got)
	if _, err := endpoint(t, server, "vision-model").Describe(context.Background(), Picture{Data: square(t, 16)}, Embedded); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Prompt, "Do NOT transcribe text") {
		t.Errorf("prompt = %q", got.Prompt)
	}
}

func TestAReasoningModelsThinkingIsLeftOut(t *testing.T) {
	server := stub(t, 200, `{"choices":[{"message":{"content":[{"type":"thinking","thinking":"hmm"},{"type":"text","text":"A cat."}]}}]}`, nil)
	text, err := endpoint(t, server, "m").Describe(context.Background(), Picture{Data: square(t, 16)}, Whole)
	if err != nil || text != "A cat." {
		t.Fatalf("description = %q, %v", text, err)
	}
}

func TestFailuresAreParseErrorsTheIndexerCanActOn(t *testing.T) {
	for name, case_ := range map[string]struct {
		status    int
		reply     string
		kind      parse.Kind
		retryable bool
	}{
		"the proxy is down":          {502, "bad gateway", parse.Unavailable, true},
		"rate limited":               {429, "slow down", parse.RateLimited, true},
		"the key is refused":         {401, "no", parse.Unauthorized, false},
		"the model said nothing":     {200, `{"choices":[{"message":{"content":"   "}}]}`, parse.Unavailable, true},
		"no answer at all":           {200, `{"choices":[]}`, parse.BadResponse, false},
		"an answer that is not JSON": {200, `<html>`, parse.BadResponse, false},
	} {
		t.Run(name, func(t *testing.T) {
			server := stub(t, case_.status, case_.reply, nil)
			_, err := endpoint(t, server, "m").Describe(context.Background(), Picture{Name: "p.png", Data: square(t, 16)}, Whole)
			var failure *parse.Error
			if !errors.As(err, &failure) || failure.Kind != case_.kind || failure.Parser != parse.Vision || failure.Retryable() != case_.retryable {
				t.Fatalf("error = %v, want a %s vision error, retryable %v", err, case_.kind, case_.retryable)
			}
		})
	}
}

func TestNoModelIsRefusedBeforeAnythingIsSent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("nothing should have been sent")
	}))
	defer server.Close()
	_, err := client(server).Endpoint(" ")
	if parse.KindOf(err) != parse.NotConfigured {
		t.Fatalf("error = %v", err)
	}
	// An endpoint put together by hand without a name is refused the same
	// way, before anything is sent.
	_, err = Endpoint{BaseURL: server.URL, Key: "k", HTTP: server.Client(), Timeout: time.Second}.Describe(context.Background(), Picture{Data: square(t, 16)}, Whole)
	if parse.KindOf(err) != parse.NotConfigured {
		t.Fatalf("error = %v", err)
	}
}

// An own connection's endpoint: its address, its key, its model name and
// its extra headers, none of which can replace the key or the body's type.
func TestAnOwnConnectionIsCalledAtItsAddressWithItsKeyAndHeaders(t *testing.T) {
	var headers http.Header
	var model string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/openai/v1/chat/completions" {
			t.Errorf("asked %s", r.URL.Path)
		}
		headers = r.Header.Clone()
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		model = body.Model
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"A red square."}}]}`)
	}))
	defer server.Close()
	own := Endpoint{
		BaseURL: server.URL + "/openai/v1/", Key: "own-key", Model: "gpt-own-vision", HTTP: server.Client(), Timeout: time.Second,
		Headers: map[string]string{"X-Org": "lab", "Authorization": "Bearer not-this-one"},
	}
	text, err := own.Describe(context.Background(), Picture{Data: square(t, 16)}, Whole)
	if err != nil || text != "A red square." {
		t.Fatalf("text %q, err %v", text, err)
	}
	if model != "gpt-own-vision" || headers.Get("Authorization") != "Bearer own-key" || headers.Get("X-Org") != "lab" ||
		headers.Get("Content-Type") != "application/json" {
		t.Fatalf("model %q, headers %v", model, headers)
	}
}

// The egress guard's refusal is said in its own sentence and is not a
// fault of the moment: retrying would be refused again.
func TestTheGuardsRefusalIsNotRetried(t *testing.T) {
	guard := errors.New("the guard said no")
	refusing := &http.Client{Transport: roundTripper(func(*http.Request) (*http.Response, error) { return nil, guard })}
	own := Endpoint{
		BaseURL: "https://models.internal/v1", Key: "k", Model: "m", HTTP: refusing, Timeout: time.Second,
		Refused: func(err error) (string, bool) {
			return "The address models.internal points inside this server's network.", errors.Is(err, guard)
		},
	}
	_, err := own.Describe(context.Background(), Picture{Data: square(t, 16)}, Whole)
	var failure *parse.Error
	if !errors.As(err, &failure) || failure.Kind != parse.Rejected || failure.Retryable() ||
		!strings.Contains(failure.Message, "points inside this server's network") {
		t.Fatalf("error = %#v", err)
	}
	// Post says the same with a *Refusal, for the callers that read their
	// own answers.
	var refusal *Refusal
	if _, err := own.Post(context.Background(), []byte(`{}`)); !errors.As(err, &refusal) || !errors.Is(err, guard) {
		t.Fatalf("Post error = %v", err)
	}
	// Any other failure to connect stays one that may pass.
	own.Refused = func(error) (string, bool) { return "", false }
	if _, err := own.Describe(context.Background(), Picture{Data: square(t, 16)}, Whole); !errors.As(err, &failure) || !failure.Retryable() {
		t.Fatalf("error = %v", err)
	}
}

type roundTripper func(*http.Request) (*http.Response, error)

func (f roundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSniffReadsTheFormatFromTheBytes(t *testing.T) {
	for want, data := range map[Format][]byte{
		PNG:     square(t, 4),
		JPEG:    {0xFF, 0xD8, 0xFF, 0xE0},
		GIF:     []byte("GIF89a...."),
		WebP:    []byte("RIFF\x00\x00\x00\x00WEBPVP8 "),
		TIFF:    []byte("II*\x00rest"),
		HEIC:    []byte("\x00\x00\x00\x18ftypheic\x00\x00"),
		JPEG2K:  []byte("\x00\x00\x00\x0cjP  \r\n\x87\nrest"),
		Unknown: []byte("%PDF-1.4"),
	} {
		if got := Sniff(data); got != want {
			t.Errorf("Sniff(%q) = %q, want %q", data[:min(len(data), 12)], got, want)
		}
	}
}

func TestABMPIsDecodedAndSentAsAJPEG(t *testing.T) {
	var raw bytes.Buffer
	picture, _, _ := image.Decode(bytes.NewReader(square(t, 32)))
	if err := bmp.Encode(&raw, picture); err != nil {
		t.Fatal(err)
	}
	format, sent, err := Prepare(raw.Bytes())
	if err != nil || format != JPEG {
		t.Fatalf("Prepare = %q, %v", format, err)
	}
	if decoded, err := jpeg.Decode(bytes.NewReader(sent)); err != nil || decoded.Bounds().Dx() != 32 {
		t.Fatalf("what is sent is not the picture as a JPEG: %v", err)
	}
}

func TestAPictureOverTheLimitIsMadeSmallerNotSkipped(t *testing.T) {
	// A PNG of noise does not compress: 1500 by 1500 is about 6.7 MB.
	side := 1500
	noisy := image.NewRGBA(image.Rect(0, 0, side, side))
	seed := uint32(1)
	for index := range noisy.Pix {
		seed = seed*1664525 + 1013904223
		noisy.Pix[index] = byte(seed >> 24)
	}
	var raw bytes.Buffer
	if err := (&png.Encoder{CompressionLevel: png.NoCompression}).Encode(&raw, noisy); err != nil {
		t.Fatal(err)
	}
	if raw.Len() <= MaxSentBytes {
		t.Fatalf("the fixture is %d bytes, not over the limit", raw.Len())
	}
	format, sent, err := Prepare(raw.Bytes())
	if err != nil || format != JPEG || len(sent) > MaxSentBytes {
		t.Fatalf("Prepare = %q, %d bytes, %v", format, len(sent), err)
	}
}

func TestAPictureThatCannotBeSentIsRefusedAsAFormat(t *testing.T) {
	for name, data := range map[string][]byte{
		"not a picture": []byte("%PDF-1.7 ..."),
		"JPEG 2000":     []byte("\x00\x00\x00\x0cjP  \r\n\x87\nrest"),
	} {
		if _, _, err := Prepare(data); parse.KindOf(err) != parse.UnsupportedFormat {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	// A header that claims more pixels than may be held is refused before
	// any of them is decoded.
	bomb := []byte("BM")
	bomb = append(bomb, make([]byte, 52)...)
	// The pixels start right after the two headers.
	bomb[10] = 54
	header := bomb[14:]
	header[0] = 40
	for index, value := range []uint32{100_000, 100_000} {
		header[4+4*index] = byte(value)
		header[5+4*index] = byte(value >> 8)
		header[6+4*index] = byte(value >> 16)
	}
	header[12], header[14] = 1, 24
	if _, _, err := Prepare(bomb); parse.KindOf(err) != parse.TooLarge {
		t.Errorf("a decompression bomb: error = %v", err)
	}
}
