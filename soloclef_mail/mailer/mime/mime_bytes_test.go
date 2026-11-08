package mime

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

// The bytes app/mailer/smtp.py's _build_mime and smtplib's own flattening
// produce, from scripts/python_mime_bytes.py. The boundaries are random on both
// sides, so each is replaced by a counted placeholder before comparing; every
// other byte is compared as it is.

type mimeFixture struct {
	Name    string `json:"name"`
	Message struct {
		To          string `json:"to"`
		Subject     string `json:"subject"`
		TextBody    string `json:"text_body"`
		HTMLBody    string `json:"html_body"`
		ReplyTo     string `json:"reply_to"`
		Attachments []struct {
			Filename      string `json:"filename"`
			ContentBase64 string `json:"content_base64"`
			ContentType   string `json:"content_type"`
		} `json:"attachments"`
	} `json:"message"`
	Sender string `json:"sender"`
	MIME   string `json:"mime"`
	Data   string `json:"data"`
}

var boundaryPattern = regexp.MustCompile(`={15}\d+==`)

func normaliseBoundaries(wire string) string {
	seen := map[string]string{}
	return boundaryPattern.ReplaceAllStringFunc(wire, func(match string) string {
		if _, ok := seen[match]; !ok {
			seen[match] = fmt.Sprintf("BOUNDARY-%d", len(seen))
		}
		return seen[match]
	})
}

func loadMIMEFixtures(t *testing.T) []mimeFixture {
	t.Helper()
	raw, err := os.ReadFile("testdata/python_mime_bytes.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []mimeFixture
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures) == 0 {
		t.Fatal("no fixtures")
	}
	return fixtures
}

func (f mimeFixture) message(t *testing.T) mailer.Message {
	t.Helper()
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
	return mailer.Message{
		To:          f.Message.To,
		Subject:     f.Message.Subject,
		TextBody:    f.Message.TextBody,
		HTMLBody:    f.Message.HTMLBody,
		ReplyTo:     f.Message.ReplyTo,
		Attachments: attachments,
	}
}

func TestTheMessageIsTheBytesPythonBuilds(t *testing.T) {
	for _, f := range loadMIMEFixtures(t) {
		t.Run(f.Name, func(t *testing.T) {
			built, err := Build(f.message(t), f.Sender, RandomBoundary)
			if err != nil {
				t.Fatal(err)
			}
			got := normaliseBoundaries(built)
			if got == f.MIME {
				return
			}
			t.Errorf("not the message Python builds\n%s", lineDiff(f.MIME, got))
		})
	}
}

// lineDiff names the first line that differs, because a whole MIME message
// side by side is unreadable and the first divergence is the one to fix.
func lineDiff(want, got string) string {
	wantLines := strings.Split(want, "\r\n")
	gotLines := strings.Split(got, "\r\n")
	for i := range max(len(wantLines), len(gotLines)) {
		var w, g string
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if w != g {
			return fmt.Sprintf("line %d\n py: %q\n go: %q", i+1, w, g)
		}
	}
	return "the lines are equal but the bytes are not"
}
