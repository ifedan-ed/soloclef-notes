// Package vision has a vision model look at a picture and say in Markdown
// what it shows: a model on the model proxy, or a model of an account's own
// OpenAI-compatible connection (Endpoint).
//
// It is what an upload's vision switch does (an uploaded picture becomes
// its description; a PDF's embedded pictures are described where they sit,
// package pictures), and what the chat's file tools do with a picture
// attachment. Every call goes to an OpenAI-style chat completions route
// with the picture as a data URL (Endpoint.Post): one request, whoever
// answers it.
//
// Which model is the caller's to open: the account's own vision choice or
// the deployment's default, read where the account is known
// (soloclef_assistant/accountvision), never here.
//
// Every failure is a *parse.Error with Parser parse.Vision, so the
// indexer's failure handling (what to tell the person, whether to try
// again) treats the vision model as it treats a parser.
package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_ingest/hostedapi"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

// Purpose is what the description is for, which decides what the model is
// asked.
type Purpose int

const (
	// Whole: the picture is the whole document (an uploaded photo, a
	// picture attached to an email). Its text is wanted too: there is
	// nothing else that would read it.
	Whole Purpose = iota + 1
	// Embedded: the picture is inside a document whose text the parser
	// already read, text drawn into pictures included. Only what it shows
	// is wanted: transcribing its labels again would pay for text the
	// document already has, twice in every search result.
	Embedded
)

// The prompts, as the Python tier asked them (etl_pipeline/parsers/
// vision_llm.py), so a picture indexed by either tier reads the same.
const (
	wholePrompt = "Describe this image in markdown. " +
		"Transcribe any visible text verbatim. " +
		"Be concise but complete — let the image content guide the level of detail."
	embeddedPrompt = "Describe what this image visually depicts in concise markdown. " +
		"Focus on visual content — anatomy, structures, charts, diagrams, " +
		"spatial relationships, colors, modality (e.g. axial CT, ECG strip, " +
		"histology slide), and any clinically or structurally relevant findings.\n\n" +
		"Do NOT transcribe text from the image. Any text in the image " +
		"(axis labels, annotations, scale bars, lab values, etc.) is " +
		"already extracted by a separate OCR pipeline; duplicating it " +
		"here would be redundant. Stick to the visual interpretation."
)

// maxAnswerBytes bounds the reply read. A description is a few kilobytes;
// a megabyte is a model that has lost its way, not a description.
const maxAnswerBytes = 1 << 20

// Client is the model proxy as the vision models behind it are reached:
// its address, its key, the HTTP client and the bound on one description.
// Endpoint gives one of its models.
type Client struct {
	// BaseURL is the proxy's OpenAI-style root, ending in /v1.
	BaseURL string
	Key     string
	// HTTP makes the requests. Its own timeout, if any, must be longer
	// than Timeout.
	HTTP *http.Client
	// Timeout bounds one description. A vision model answers in seconds;
	// one silent for two minutes is not going to.
	Timeout time.Duration
}

// Check reports what is missing. Called once at start-up.
func (c Client) Check() error {
	address, err := url.Parse(c.BaseURL)
	switch {
	case err != nil || address.Host == "" || (address.Scheme != "http" && address.Scheme != "https"):
		return fmt.Errorf("vision: the proxy address %q is not an http(s) URL", c.BaseURL)
	case c.Key == "":
		return errors.New("vision: the proxy key is not set")
	case c.HTTP == nil || c.Timeout <= 0:
		return errors.New("vision: the HTTP client and the timeout are both required")
	}
	return nil
}

// Endpoint is the proxy's model by this name. It calls nothing.
func (c Client) Endpoint(model string) (Endpoint, error) {
	if strings.TrimSpace(model) == "" {
		// The caller decides which model; asking with none is the caller's
		// bug, and is said so rather than sent for the proxy to guess.
		return Endpoint{}, &parse.Error{Kind: parse.NotConfigured, Parser: parse.Vision, Message: "no vision model was named"}
	}
	return Endpoint{BaseURL: c.BaseURL, Key: c.Key, Model: model, HTTP: c.HTTP, Timeout: c.Timeout}, nil
}

// Describer is one vision model, bound to its address when it was opened,
// asked what a picture shows: Endpoint. The request names no model, so
// there is nowhere for a second model to be swapped in.
type Describer interface {
	Describe(ctx context.Context, picture Picture, purpose Purpose) (string, error)
}

// Endpoint is one vision model and where it is called: a name on the
// proxy (Client.Endpoint), or a model of an account's own OpenAI-compatible
// connection at the address the person gave (soloclef_assistant/ownmodels
// opens those, with their key, their headers and the egress guard's
// client). Both speak the same chat completions route, so one request
// serves both, and every caller that shows a vision model a picture (the
// descriptions here, a document's page review, an infographic's read-back,
// a video's stills) sends it through Post.
type Endpoint struct {
	// BaseURL is the OpenAI-style root, ending in /v1 or the endpoint's
	// own equivalent; /chat/completions is added to it.
	BaseURL string
	Key     string
	// Model is the name the body carries: the proxy's name, or the
	// endpoint's own name for the model.
	Model string
	// Headers are an own connection's extra headers, sent before this
	// package's own, which they cannot replace. Nil for the proxy.
	Headers map[string]string
	// HTTP makes the request. For an own connection it is the egress
	// guard's client, which connects only to the address the person gave
	// and never inside this server's network.
	HTTP *http.Client
	// Timeout bounds one description (Describe). Post leaves the bound to
	// its caller, whose requests carry anything from one picture to twenty
	// pages.
	Timeout time.Duration
	// Refused reads a failed request as the egress guard's refusal of the
	// address, and gives the sentence for it: such a failure is not a
	// model that is down and will be refused again, so it must not be
	// retried as one. Nil for the proxy, which is the deployment's own
	// address and has no guard.
	Refused func(err error) (sentence string, refused bool)
}

// Refusal is a request the endpoint's egress guard would not make: the
// address resolves inside this server's network. Sentence is for the
// person; it will be the same the next time.
type Refusal struct {
	Sentence string
	Cause    error
}

func (r *Refusal) Error() string { return r.Sentence }
func (r *Refusal) Unwrap() error { return r.Cause }

// Post sends one chat completions request with body, which the caller
// wrote and which names e.Model. The caller closes the answer's body and
// reads the status. A failure to get an answer is the transport's error,
// or a *Refusal when the guard refused the address.
func (e Endpoint) Post(ctx context.Context, body []byte) (*http.Response, error) {
	if e.HTTP == nil {
		return nil, errors.New("vision: the endpoint has no HTTP client")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(e.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// The connection's own headers first, then this package's: a header
	// may add to a request, never change its key or its body's type (the
	// settings screen refuses such a header when it is saved, too).
	for name, value := range e.Headers {
		request.Header.Set(name, value)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+e.Key)
	response, err := e.HTTP.Do(request)
	if err != nil && e.Refused != nil {
		if sentence, refused := e.Refused(err); refused {
			return nil, &Refusal{Sentence: sentence, Cause: err}
		}
	}
	return response, err
}

// Picture is one picture to describe. Name is for the error messages only:
// the model is not told it, since a file name is not what a picture shows.
type Picture struct {
	Name string
	Data []byte
}

// Describe is what the endpoint's model says the picture shows, in
// Markdown.
func (e Endpoint) Describe(ctx context.Context, picture Picture, purpose Purpose) (string, error) {
	if strings.TrimSpace(e.Model) == "" || e.Timeout <= 0 {
		return "", &parse.Error{Kind: parse.NotConfigured, Parser: parse.Vision, Message: "the vision model was opened without a name or a time limit"}
	}
	prompt := wholePrompt
	if purpose == Embedded {
		prompt = embeddedPrompt
	}
	format, data, err := Prepare(picture.Data)
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(map[string]any{
		"model":  e.Model,
		"stream": false,
		"messages": []map[string]any{{"role": "user", "content": []map[string]any{
			{"type": "text", "text": prompt},
			{"type": "image_url", "image_url": map[string]string{
				"url": "data:" + string(format) + ";base64," + base64.StdEncoding.EncodeToString(data),
			}},
		}}},
	})
	if err != nil {
		return "", &parse.Error{Kind: parse.Rejected, Parser: parse.Vision, Message: "the request could not be written", Err: err}
	}

	ctx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()
	response, err := e.Post(ctx, body)
	var refused *Refusal
	switch {
	case errors.As(err, &refused):
		// Not retryable: the address will resolve inside the network again.
		return "", &parse.Error{Kind: parse.Rejected, Parser: parse.Vision, Message: refused.Sentence, Err: err}
	case err != nil:
		return "", hostedapi.TransportError(ctx, parse.Vision, "describing "+nameOf(picture), err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", hostedapi.StatusError(parse.Vision, "describing "+nameOf(picture), response)
	}
	var answer struct {
		Choices []struct {
			Message struct {
				// A string from most models; a list of parts, thinking
				// first, from a reasoning model.
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := hostedapi.DecodeJSON(parse.Vision, "describing "+nameOf(picture), response.Body, maxAnswerBytes, &answer); err != nil {
		return "", err
	}
	if len(answer.Choices) == 0 {
		return "", &parse.Error{Kind: parse.BadResponse, Parser: parse.Vision, Message: "the vision model gave no answer for " + nameOf(picture)}
	}
	text := strings.TrimSpace(ReplyText(answer.Choices[0].Message.Content))
	if text == "" {
		// Retryable as a fault of the moment: a model that answered nothing
		// usually answers the same picture the next time.
		return "", &parse.Error{Kind: parse.Unavailable, Parser: parse.Vision, Message: "the vision model answered nothing for " + nameOf(picture)}
	}
	return text, nil
}

// ReplyText is the text of a reply's content: the string, or the text
// parts of a list of parts (a reasoning model's thinking parts are left
// out: they are how it got there, not the answer). Every caller that reads
// a vision model's answer reads it with this, so a reasoning model on an
// own connection answers each of them as it answers a description.
func ReplyText(content json.RawMessage) string {
	var text string
	if json.Unmarshal(content, &text) == nil {
		return text
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(content, &parts) != nil {
		return ""
	}
	var joined []string
	for _, part := range parts {
		if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
			joined = append(joined, part.Text)
		}
	}
	return strings.Join(joined, "\n\n")
}

func nameOf(picture Picture) string {
	if picture.Name == "" {
		return "a picture"
	}
	return picture.Name
}
