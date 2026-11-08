package smtp_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
	smtptransport "git.danvics.com/danvics/soloclef/soloclef_mail/mailer/smtp"
)

// Does a receiving server get the same message from this sender as from the
// Python one?
//
// The MIME bytes are already compared against Python's own builder in
// mailer/mime, but that comparison never opens a socket: it cannot catch a
// dot-stuffing mistake, a line ending turned on the wire, an 8-bit body a
// transport refused to carry, or a header a server reads differently than it
// was written. So the same message goes through both senders into one throwaway
// Mailpit, and what Mailpit stored is compared.
//
// Nothing is defaulted. This package and service share one Mailpit and each
// clears it, so they are run with -p 1; the commands are in
// soloclef_mail/AGENTS.md.

type sinkEnv struct {
	api       string
	host      string
	port      int
	image     string
	network   string
	fromImage string
}

func sinkSettings(t *testing.T) sinkEnv {
	t.Helper()
	api := os.Getenv("SOLOCLEF_MAIL_TEST_MAILPIT_API")
	host := os.Getenv("SOLOCLEF_MAIL_TEST_SMTP_HOST")
	port, _ := strconv.Atoi(os.Getenv("SOLOCLEF_MAIL_TEST_SMTP_PORT"))
	env := sinkEnv{
		api:       api,
		host:      host,
		port:      port,
		image:     os.Getenv("SOLOCLEF_MAIL_TEST_IMAGE"),
		network:   os.Getenv("SOLOCLEF_MAIL_TEST_NETWORK"),
		fromImage: os.Getenv("SOLOCLEF_MAIL_TEST_SMTP_FROM_CONTAINER"),
	}
	if api == "" || host == "" || port == 0 || env.image == "" || env.network == "" || env.fromImage == "" {
		t.Skip("set SOLOCLEF_MAIL_TEST_MAILPIT_API, _SMTP_HOST, _SMTP_PORT, _SMTP_FROM_CONTAINER, _IMAGE and _NETWORK to compare against the Python sender")
	}
	return env
}

const defaultSender = "notes@queue.test"

type sinkCase struct {
	name    string
	sender  string
	message mailer.Message
}

func sinkCases() []sinkCase {
	long := "Your automation “Weekly reading digest for the research notebook” " +
		"— 2026-11-08 finished, and this subject is long enough that a header has to fold"
	return []sinkCase{
		{"plain ascii", "", mailer.Message{
			To: "person@queue.test", Subject: "Reset your password",
			TextBody: "Use the link to set a new password.\n"}},
		{"non ascii subject", "", mailer.Message{
			To: "person@queue.test", Subject: "Résumé — über 漢字",
			TextBody: "Plain ascii body.\n"}},
		{"non ascii display name", "Soloclef Nötes — Team <notes@queue.test>", mailer.Message{
			To: "person@queue.test", Subject: "Invitation", TextBody: "Plain ascii body.\n"}},
		{"long subject folds", "", mailer.Message{
			To: "person@queue.test", Subject: long, TextBody: "x\n"}},
		{"long non ascii subject folds", "", mailer.Message{
			To: "person@queue.test", Subject: "Über " + long, TextBody: "x\n"}},
		{"emoji subject", "", mailer.Message{
			To: "person@queue.test", Subject: "Done \U0001f600 — ok", TextBody: "x\n"}},
		{"non ascii body goes out 8bit", "", mailer.Message{
			To: "person@queue.test", Subject: "Grüße",
			TextBody: "Grüße aus München.\n nbsp here\n"}},
		{"body with dot lines", "", mailer.Message{
			To: "person@queue.test", Subject: "dots",
			TextBody: ".dot first\nsecond\n.\n..double\nlast.\n"}},
		{"body with crlf already", "", mailer.Message{
			To: "person@queue.test", Subject: "crlf",
			TextBody: "one\r\ntwo\r\n.\r\nthree\n"}},
		{"long line becomes quoted printable", "", mailer.Message{
			To: "person@queue.test", Subject: "long line",
			TextBody: strings.Repeat("x", 200) + "\nshort\n"}},
		{"html alternative", "Soloclef Notes <notes@queue.test>", mailer.Message{
			To: "person@queue.test", Subject: "You are invited",
			TextBody: "Open the link.\n", HTMLBody: "<p>Open the <a href=\"x\">link</a>.</p>",
			ReplyTo: "Soloclef Support <support@queue.test>"}},
		{"attachment", "", mailer.Message{
			To: "person@queue.test", Subject: "Your export", TextBody: "Attached.\n",
			Attachments: []mailer.Attachment{{
				Filename:    "export.txt",
				Content:     []byte("-----BEGIN FILE-----\nCONTRACT-BYTES\n-----END FILE-----\n"),
				ContentType: "application/octet-stream"}}}},
		{"html and two attachments", "", mailer.Message{
			To: "person@queue.test", Subject: "Your export", TextBody: "Attached.\n",
			HTMLBody: "<p>Attached.</p>",
			Attachments: []mailer.Attachment{
				{Filename: "reçu é.txt", Content: append(byteRange(), []byte("\xff\xfe\x00END")...),
					ContentType: "application/octet-stream"},
				{Filename: "tiny.png", Content: []byte("\x89PNG\r\n\x1a\n"), ContentType: "image/png"}}}},
	}
}

func byteRange() []byte {
	raw := make([]byte, 48)
	for i := range raw {
		raw[i] = byte(i)
	}
	return raw
}

func TestWhatMailpitReceivesIsWhatThePythonSenderDelivers(t *testing.T) {
	env := sinkSettings(t)
	for _, c := range sinkCases() {
		t.Run(c.name, func(t *testing.T) {
			clearSink(t, env)
			pythonSend(t, env, c)
			fromPython := rawMessage(t, env)

			clearSink(t, env)
			goSend(t, env, c)
			fromGo := rawMessage(t, env)

			if normalise(fromGo) != normalise(fromPython) {
				t.Errorf("Mailpit received a different message\n%s",
					firstDifference(normalise(fromPython), normalise(fromGo)))
			}
		})
	}
}

func goSend(t *testing.T, env sinkEnv, c sinkCase) {
	t.Helper()
	m, warning, err := smtptransport.New(smtptransport.Settings{
		Host: env.host, Port: env.port, Security: smtptransport.Insecure,
		From: defaultSender, Timeout: 10 * time.Second, HelloName: "queue.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if env.host != "127.0.0.1" && warning == "" {
		t.Error("SMTP_SECURITY=none to a non-loopback host must warn")
	}
	defer m.Close()
	message := c.message
	message.Sender = c.sender
	if err := m.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
}

func pythonSend(t *testing.T, env sinkEnv, c sinkCase) {
	t.Helper()
	attachments := make([]map[string]any, 0, len(c.message.Attachments))
	for _, a := range c.message.Attachments {
		attachments = append(attachments, map[string]any{
			"filename":       a.Filename,
			"content_base64": base64.StdEncoding.EncodeToString(a.Content),
			"content_type":   a.ContentType,
		})
	}
	host, port, _ := strings.Cut(env.fromImage, ":")
	portNumber, _ := strconv.Atoi(port)
	payload := map[string]any{
		"host":           host,
		"port":           portNumber,
		"default_sender": defaultSender,
		"sender":         c.sender,
		"message": map[string]any{
			"to":          c.message.To,
			"subject":     c.message.Subject,
			"text_body":   c.message.TextBody,
			"html_body":   c.message.HTMLBody,
			"reply_to":    c.message.ReplyTo,
			"attachments": attachments,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	here, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("docker", "run", "--rm", "-i",
		"--network", env.network, "-e", "PYTHONPATH=/app", "-w", "/app",
		"-v", here+"/../../scripts/python_smtp_send.py:/tmp/send.py:ro",
		env.image, "python", "/tmp/send.py")
	cmd.Stdin = strings.NewReader(string(body))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the app's own SmtpMailer did not send: %v\n%s", err, out)
	}
}

func clearSink(t *testing.T, env sinkEnv) {
	t.Helper()
	request, err := http.NewRequest(http.MethodDelete, env.api+"/api/v1/messages", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	io.Copy(io.Discard, response.Body)
}

// rawMessage is what the receiving server stored: the message as it arrived,
// not Mailpit's parse of it.
func rawMessage(t *testing.T, env sinkEnv) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		listing := get(t, env.api+"/api/v1/messages?limit=5")
		var answer struct {
			Messages []struct {
				ID string `json:"ID"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(listing), &answer); err != nil {
			t.Fatal(err)
		}
		if len(answer.Messages) == 1 {
			return get(t, env.api+"/api/v1/message/"+answer.Messages[0].ID+"/raw")
		}
		if len(answer.Messages) > 1 {
			t.Fatalf("one send delivered %d messages", len(answer.Messages))
		}
		if time.Now().After(deadline) {
			t.Fatal("nothing arrived at the sink")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func get(t *testing.T, url string) string {
	t.Helper()
	response, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("%s answered %d", url, response.StatusCode)
	}
	return string(raw)
}

var (
	boundaryPattern = regexp.MustCompile(`={15}\d+==`)
	// What the receiving server adds for itself, which says nothing about
	// either sender.
	receivedPattern = regexp.MustCompile(`(?mi)^(received|message-id|return-path|delivered-to|x-mailpit[^:]*):.*(\r?\n[ \t].*)*\r?\n`)
)

func normalise(wire string) string {
	seen := map[string]string{}
	wire = receivedPattern.ReplaceAllString(wire, "")
	return boundaryPattern.ReplaceAllStringFunc(wire, func(match string) string {
		if _, ok := seen[match]; !ok {
			seen[match] = fmt.Sprintf("BOUNDARY-%d", len(seen))
		}
		return seen[match]
	})
}

func firstDifference(want, got string) string {
	wantLines := strings.Split(want, "\n")
	gotLines := strings.Split(got, "\n")
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
