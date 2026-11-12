package deliverables

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// The two models this package asks for pictures: one that draws an image
// from a prompt, and one that reads an image back. Both are reached through
// the model proxy and nowhere else, each under one name a deployment sets.
// Neither has a second choice: a model that is not set is said to be not
// set, and one that fails is said to have failed.
//
// (The chat this replaces picked an image model per account, fell back to
// "auto" when none was chosen, and retried a failed call on a second model.
// A person could not tell which model had drawn their image.)

// GeneratedImage is one image as the model returned it.
type GeneratedImage struct {
	// Data is the file's bytes; MimeType and Extension are read from them,
	// never taken from the provider's word.
	Data      []byte
	MimeType  string
	Extension string
	// RevisedPrompt is the prompt the provider says it actually drew from,
	// when it rewrote the one it was given. Otherwise "".
	RevisedPrompt string
}

// ImageModel draws an image.
//
// An error that is an *ImageRefusal carries a sentence the person may be
// told. Any other error is a failure whose text stays in the log.
type ImageModel interface {
	// Check reports what is missing. Called once at start-up.
	Check() error
	// Name is the proxy's name for the model, for an artifact's record of
	// what drew it. "" for a deployment with none.
	Name() string
	// Generate draws one image. size is "WIDTHxHEIGHT", or "" for the
	// model's own.
	Generate(ctx context.Context, prompt, size string) (GeneratedImage, error)
}

// ImageRefusal is an image that was not made for a reason the person asking
// should hear.
type ImageRefusal struct{ Reason string }

func (r *ImageRefusal) Error() string { return r.Reason }

// What a person is told when an image cannot be made.
const (
	noImageModel          = "This server has no image model set up, so images cannot be made here."
	imageRequestRefused   = "The image model would not draw this request. Rewording it may help."
	imageModelBusy        = "The image model is busy just now. Try again in a moment."
	imageModelMisset      = "The image model is not set up correctly on this server."
	imageModelUnusable    = "The image model returned something that is not a usable image."
	imageModelUnavailable = "The image model could not be reached just now. Try again in a moment."
)

// NoImageModel is the ImageModel of a deployment that has set none. It is a
// value a deployment chooses on purpose (its image model setting is empty),
// so that "no image model" is a sentence the person is told and never a
// request sent to some other model.
type NoImageModel struct{}

func (NoImageModel) Check() error { return nil }
func (NoImageModel) Name() string { return "" }
func (NoImageModel) Generate(context.Context, string, string) (GeneratedImage, error) {
	return GeneratedImage{}, &ImageRefusal{Reason: noImageModel}
}

// ProxyImages is ImageModel over the model proxy's image route
// (POST {BaseURL}/images/generations, the OpenAI shape). Every field is
// required.
type ProxyImages struct {
	// BaseURL is the proxy's OpenAI-compatible root, ending in /v1.
	BaseURL string
	Key     string
	// Model is the proxy's name for the image model.
	Model string
	HTTP  *http.Client
	// Timeout bounds one image: the request and, when the provider answers
	// with an address instead of the bytes, the download.
	Timeout time.Duration
	// MaxBytes is the largest image accepted.
	MaxBytes int64
}

func (p ProxyImages) Check() error {
	switch {
	case strings.TrimSpace(p.BaseURL) == "":
		return errors.New("deliverables: ProxyImages.BaseURL is not set")
	case p.Key == "":
		return errors.New("deliverables: ProxyImages.Key is not set")
	case p.Model == "":
		return errors.New("deliverables: ProxyImages.Model is not set")
	case p.HTTP == nil:
		return errors.New("deliverables: ProxyImages.HTTP is not set")
	case p.Timeout <= 0:
		return fmt.Errorf("deliverables: ProxyImages.Timeout must be positive, got %s", p.Timeout)
	case p.MaxBytes < 1:
		return fmt.Errorf("deliverables: ProxyImages.MaxBytes must be positive, got %d", p.MaxBytes)
	}
	return nil
}

func (p ProxyImages) Name() string { return p.Model }

// maxProxyErrorBytes bounds how much of a failed response is read for the
// log.
const maxProxyErrorBytes = 2000

func (p ProxyImages) Generate(ctx context.Context, prompt, size string) (GeneratedImage, error) {
	ctx, cancel := context.WithTimeout(ctx, p.Timeout)
	defer cancel()
	image, err := p.generate(ctx, prompt, size)
	if err != nil {
		// Every failure leaves here worded for the person, with its cause
		// inside for the log.
		return GeneratedImage{}, imageFailure(ctx, err)
	}
	return image, nil
}

// generate is Generate without the wording: its errors are the causes.
func (p ProxyImages) generate(ctx context.Context, prompt, size string) (GeneratedImage, error) {

	// One image per call, always. The route can be asked for several, and
	// the tool this replaces let the model ask for four and then kept the
	// first: three images paid for and thrown away.
	request := map[string]any{"model": p.Model, "prompt": prompt, "n": 1}
	if size != "" {
		request["size"] = size
	}
	body, err := json.Marshal(request)
	if err != nil {
		return GeneratedImage{}, err
	}
	raw, err := proxyPost(ctx, p.HTTP, strings.TrimRight(p.BaseURL, "/")+"/images/generations", p.Key, body, p.jsonLimit())
	if err != nil {
		return GeneratedImage{}, err
	}
	var answer struct {
		Data []struct {
			B64           string `json:"b64_json"`
			URL           string `json:"url"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil || len(answer.Data) == 0 {
		return GeneratedImage{}, fmt.Errorf("%w: the image route answered no image", errUnusableImage)
	}
	first := answer.Data[0]
	var data []byte
	switch {
	case first.B64 != "":
		if data, err = base64.StdEncoding.DecodeString(first.B64); err != nil {
			return GeneratedImage{}, fmt.Errorf("%w: b64_json is not base64", errUnusableImage)
		}
	case first.URL != "":
		// A provider's address for the image is short-lived, so the bytes
		// are fetched now. The address came from the proxy, which this tier
		// trusts with every model call; it is not one a model or a person
		// chose.
		if data, err = p.download(ctx, first.URL); err != nil {
			return GeneratedImage{}, err
		}
	default:
		return GeneratedImage{}, fmt.Errorf("%w: the image has neither b64_json nor url", errUnusableImage)
	}
	if int64(len(data)) > p.MaxBytes {
		return GeneratedImage{}, fmt.Errorf("%w: the image is %d bytes, over the %d allowed", errUnusableImage, len(data), p.MaxBytes)
	}
	mime, extension, known := imageType(data)
	if !known {
		return GeneratedImage{}, fmt.Errorf("%w: the bytes are not a PNG, a JPEG or a WebP", errUnusableImage)
	}
	revised := strings.TrimSpace(first.RevisedPrompt)
	if revised == strings.TrimSpace(prompt) {
		revised = ""
	}
	return GeneratedImage{Data: data, MimeType: mime, Extension: extension, RevisedPrompt: revised}, nil
}

// jsonLimit is the largest response body read: the image as base64 (four
// bytes for three) and room for the JSON around it.
func (p ProxyImages) jsonLimit() int64 { return p.MaxBytes/3*4 + 64<<10 }

func (p ProxyImages) download(ctx context.Context, address string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: the image's address is not usable", errUnusableImage)
	}
	response, err := p.HTTP.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, &proxyStatus{status: response.StatusCode, body: "downloading the generated image"}
	}
	// One byte past the limit is read so that "too large" can be told from
	// "exactly the limit".
	data, err := io.ReadAll(io.LimitReader(response.Body, p.MaxBytes+1))
	if err != nil {
		return nil, err
	}
	return data, nil
}

// errUnusableImage marks an answer that arrived and is not an image.
var errUnusableImage = errors.New("deliverables: unusable image")

// imageType reads an image's type from its first bytes.
func imageType(data []byte) (mime, extension string, known bool) {
	switch {
	case bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png", "png", true
	case bytes.HasPrefix(data, []byte("\xff\xd8\xff")):
		return "image/jpeg", "jpg", true
	case len(data) >= 12 && string(data[:4]) == "RIFF" && string(data[8:12]) == "WEBP":
		return "image/webp", "webp", true
	}
	return "", "", false
}

// proxyStatus is the proxy, or a vision model's endpoint, answering with
// something other than 200.
type proxyStatus struct {
	// from names who answered, for the log: "the model proxy", or "the
	// vision model" for an endpoint that may be an account's own.
	from   string
	status int
	// body is the start of what it said, for the log only: a proxy's error
	// names providers, models and sometimes keys' owners.
	body string
}

func (e *proxyStatus) Error() string {
	return fmt.Sprintf("%s answered %d: %s", e.from, e.status, e.body)
}

// proxyPost sends one JSON request to the model proxy and returns the body
// of a 200.
func proxyPost(ctx context.Context, client *http.Client, address, key string, body []byte, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	return answerOf(response, "the model proxy", limit)
}

// answerOf is the body of a 200, at most limit bytes, and closes it.
func answerOf(response *http.Response, from string, limit int64) ([]byte, error) {
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		text, _ := io.ReadAll(io.LimitReader(response.Body, maxProxyErrorBytes))
		return nil, &proxyStatus{from: from, status: response.StatusCode, body: strings.Join(strings.Fields(string(text)), " ")}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("%w: the answer is larger than the %d bytes allowed", errUnusableImage, limit)
	}
	return raw, nil
}

// imageFailure words a failed image for the person, by who can fix it: the
// person (a refused prompt), nobody (busy, unreachable: wait), the operator
// (a wrong key or model name). The cause is kept inside the refusal's error
// chain for the log.
func imageFailure(ctx context.Context, err error) error {
	if ctx.Err() != nil && !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		// The turn was stopped; that is not the image model's failure.
		return ctx.Err()
	}
	var status *proxyStatus
	reason := imageModelUnavailable
	switch {
	case errors.Is(err, errUnusableImage):
		reason = imageModelUnusable
	case errors.As(err, &status):
		switch {
		case status.status == http.StatusBadRequest || status.status == http.StatusUnprocessableEntity:
			reason = imageRequestRefused
		case status.status == http.StatusTooManyRequests:
			reason = imageModelBusy
		case status.status == http.StatusUnauthorized || status.status == http.StatusForbidden || status.status == http.StatusNotFound:
			reason = imageModelMisset
		}
	}
	return &causedRefusal{ImageRefusal: ImageRefusal{Reason: reason}, cause: err}
}

// causedRefusal is an ImageRefusal that remembers why, for the log.
type causedRefusal struct {
	ImageRefusal
	cause error
}

func (r *causedRefusal) Unwrap() error { return r.cause }

// As lets errors.As find the plain *ImageRefusal inside.
func (r *causedRefusal) As(target any) bool {
	if refusal, ok := target.(**ImageRefusal); ok {
		*refusal = &r.ImageRefusal
		return true
	}
	return false
}

// ImageReader shows a model an image and a question about it, and answers
// what the model wrote. It is how an infographic's text is read back.
type ImageReader interface {
	// Check reports what is missing. Called once at start-up.
	Check() error
	// Read asks prompt of the image, for the account whose infographic it
	// is. ErrNoImageReader means neither the account nor the deployment
	// has a model that reads images.
	Read(ctx context.Context, userID, prompt string, image []byte, mimeType string) (string, error)
}

// ErrNoImageReader is the answer for an account with no model that reads
// images: it chose none, and the deployment has none.
var ErrNoImageReader = errors.New("deliverables: no model that reads images is set up")

// NoImageReader is the ImageReader that reads nothing back: every
// infographic made with it is saved as not checked, and says so. A
// deployment's specialist reads with VisionImageReader, whose account may
// have a model even when the deployment has none; this is for a specialist
// built where no picture is ever read back, as the other tiers' tests of
// the chat are.
type NoImageReader struct{}

func (NoImageReader) Check() error { return nil }
func (NoImageReader) Read(context.Context, string, string, []byte, string) (string, error) {
	return "", ErrNoImageReader
}

// VisionImageReader is ImageReader over the account's vision model
// (Sight): a proxy model or one of its own connection, the image sent
// inline. Every field is required.
type VisionImageReader struct {
	Sight Sight
	// Timeout bounds one reading.
	Timeout time.Duration
}

func (v VisionImageReader) Check() error {
	switch {
	case v.Sight == nil:
		return errors.New("deliverables: VisionImageReader.Sight is not set")
	case v.Timeout <= 0:
		return fmt.Errorf("deliverables: VisionImageReader.Timeout must be positive, got %s", v.Timeout)
	}
	return nil
}

// maxReadingBytes bounds the answer to a question about an image.
const maxReadingBytes = 1 << 20

func (v VisionImageReader) Read(ctx context.Context, userID, prompt string, image []byte, mimeType string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, v.Timeout)
	defer cancel()
	endpoint, err := openSight(ctx, v.Sight, userID)
	if errors.Is(err, errNoSight) {
		return "", ErrNoImageReader
	}
	if err != nil {
		return "", err
	}
	return askSight(ctx, endpoint, []sightPart{{Type: "text", Text: prompt}, pictureOf(mimeType, image)}, maxReadingBytes, "the model that reads images")
}
