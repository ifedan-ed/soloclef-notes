package smtp2go

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

// The request body is compared with the bytes app/mailer/smtp2go_api.py's
// _payload() produces once httpx has encoded it, because the encoding is
// httpx's own (ensure_ascii=False, compact separators) and Go's encoder
// disagrees with it on the HTML in every message body. The fixtures come from
// scripts/python_smtp2go_payload.py, run in the backend image.

type bodyFixture struct {
	Name    string `json:"name"`
	Message struct {
		To             string `json:"to"`
		Subject        string `json:"subject"`
		TextBody       string `json:"text_body"`
		HTMLBody       string `json:"html_body"`
		ReplyTo        string `json:"reply_to"`
		IdempotencyKey string `json:"idempotency_key"`
		Attachments    []struct {
			Filename      string `json:"filename"`
			ContentBase64 string `json:"content_base64"`
			ContentType   string `json:"content_type"`
		} `json:"attachments"`
	} `json:"message"`
	Sender        string `json:"sender"`
	Expected      string `json:"expected"`
	ContentType   string `json:"content_type"`
	ContentLength string `json:"content_length"`
}

func TestTheRequestBodyIsTheBytesPythonSends(t *testing.T) {
	raw, err := os.ReadFile("testdata/python_request_body.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []bodyFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures")
	}

	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			var attachments []mailer.Attachment
			for _, a := range f.Message.Attachments {
				content, err := base64.StdEncoding.DecodeString(a.ContentBase64)
				if err != nil {
					t.Fatal(err)
				}
				attachments = append(attachments, mailer.Attachment{
					Filename:    a.Filename,
					Content:     content,
					ContentType: a.ContentType,
				})
			}
			message := mailer.Message{
				To:             f.Message.To,
				Subject:        f.Message.Subject,
				TextBody:       f.Message.TextBody,
				HTMLBody:       f.Message.HTMLBody,
				ReplyTo:        f.Message.ReplyTo,
				Attachments:    attachments,
				IdempotencyKey: f.Message.IdempotencyKey,
			}

			got := requestBody(message, f.Sender)
			if got != f.Expected {
				t.Errorf("not the bytes Python sends\n go: %s\n py: %s", got, f.Expected)
			}
			if length := strconv.Itoa(len(got)); length != f.ContentLength {
				t.Errorf("Content-Length %s, Python sends %s", length, f.ContentLength)
			}
		})
	}
}

func TestTheIdempotencyKeyIsNotSent(t *testing.T) {
	// It is carried on the message and read by nothing, here as in Python. The
	// API takes no such field, so inventing one would be a request smtp2go has
	// not been asked about -- and would quietly change what a retry does.
	body := requestBody(mailer.Message{
		To: "person@queue.test", Subject: "s", TextBody: "t",
		IdempotencyKey: "idem-1",
	}, "notes@queue.test")
	if contains(body, "idem-1") || contains(body, "idempotency") {
		t.Errorf("the key reached the wire: %s", body)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
