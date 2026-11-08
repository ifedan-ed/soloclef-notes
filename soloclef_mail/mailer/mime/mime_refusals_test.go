package mime

import (
	"net/textproto"
	"strings"
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

func TestAHeaderCarryingALineEndingIsRefused(t *testing.T) {
	// email.policy.default raises ValueError when such a header is set, so
	// _build_mime raises and the message is dropped rather than sent. The
	// subject of an automation result email is the automation's own name,
	// which someone typed.
	injected := "Reset\r\nBcc: someone@elsewhere.test"
	cases := map[string]struct {
		message mailer.Message
		sender  string
	}{
		"subject":   {mailer.Message{To: "a@b.test", Subject: injected, TextBody: "x"}, "notes@queue.test"},
		"recipient": {mailer.Message{To: "a@b.test\r\nBcc: x@y.test", Subject: "s", TextBody: "x"}, "notes@queue.test"},
		"reply to":  {mailer.Message{To: "a@b.test", Subject: "s", TextBody: "x", ReplyTo: injected}, "notes@queue.test"},
		"sender":    {mailer.Message{To: "a@b.test", Subject: "s", TextBody: "x"}, "notes@queue.test\r\nBcc: x@y.test"},
		"bare lf":   {mailer.Message{To: "a@b.test", Subject: "a\nb", TextBody: "x"}, "notes@queue.test"},
	}
	for name, c := range cases {
		if _, err := Build(c.message, c.sender, RandomBoundary); err == nil {
			t.Errorf("%s: a header with a line ending in it was built", name)
		}
	}
}

func TestTheWireDataIsWhatSmtplibWouldHaveSent(t *testing.T) {
	// smtplib dot-stuffs with _quote_periods and ends the data with CRLF "."
	// CRLF. Go's textproto.DotWriter is what net/smtp uses for the same job,
	// so the two are compared over every fixture: a missed dot is a truncated
	// email, and a doubled one is a line of body text that reads ".." to the
	// recipient.
	for _, f := range loadMIMEFixtures(t) {
		t.Run(f.Name, func(t *testing.T) {
			built, err := Build(f.message(t), f.Sender, RandomBoundary)
			if err != nil {
				t.Fatal(err)
			}
			var out strings.Builder
			writer := textproto.NewWriter(newBufWriter(&out))
			dot := writer.DotWriter()
			if _, err := dot.Write([]byte(built)); err != nil {
				t.Fatal(err)
			}
			if err := dot.Close(); err != nil {
				t.Fatal(err)
			}
			got := normaliseBoundaries(out.String())
			if !strings.HasSuffix(got, "\r\n.\r\n") || len(got) <= len(normaliseBoundaries(built)) {
				t.Fatalf("the dot writer produced nothing to compare: %q", got)
			}
			if got != f.Data {
				t.Errorf("not the DATA Python sends\n%s", lineDiff(f.Data, got))
			}
		})
	}
}
