package smtp_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
	smtptransport "git.danvics.com/danvics/soloclef/soloclef_mail/mailer/smtp"
)

func aMessage() mailer.Message {
	return mailer.Message{To: "person@queue.test", Subject: "s", TextBody: "t\n"}
}

func sender(t *testing.T, server *fakeServer) *smtptransport.Mailer {
	t.Helper()
	m, _, err := smtptransport.New(smtptransport.Settings{
		Host: "127.0.0.1", Port: server.port(), Security: smtptransport.Insecure,
		From: "notes@queue.test", Timeout: 5 * time.Second, HelloName: "queue.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

func TestWhichStepWasRefusedDecidesWhatHappensToTheMessage(t *testing.T) {
	cases := []struct {
		name   string
		refuse map[string]string
		want   error
	}{
		// The sender is ours: a 5xx is almost always an unverified From
		// domain. Reporting it as a rejected recipient would tell someone
		// their own address is bad.
		{"sender refused for good", map[string]string{"MAIL": "550 sender not allowed"}, mailer.ErrMisconfigured},
		{"sender refused for now", map[string]string{"MAIL": "421 slow down"}, mailer.ErrUnavailable},
		{"recipient refused for good", map[string]string{"RCPT": "550 no such user"}, mailer.ErrRejected},
		{"recipient refused for now", map[string]string{"RCPT": "451 mailbox busy"}, mailer.ErrUnavailable},
		{"data refused for good", map[string]string{"DATA": "552 too big"}, mailer.ErrRejected},
		{"data refused for now", map[string]string{"DATA": "451 try later"}, mailer.ErrUnavailable},
		{"the message refused at the end of data", map[string]string{"DATA-END": "554 spam"}, mailer.ErrRejected},
		{"the message deferred at the end of data", map[string]string{"DATA-END": "450 greylisted"}, mailer.ErrUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			server := startFakeServer(t, c.refuse, nil)
			err := sender(t, server).Send(context.Background(), aMessage())
			if !errors.Is(err, c.want) {
				t.Fatalf("got %v, want %v", err, c.want)
			}
		})
	}
}

func TestASessionIsKeptAcrossMessagesAndRenewedAfterAHundred(t *testing.T) {
	server := startFakeServer(t, nil, nil)
	m := sender(t, server)

	for range 150 {
		if err := m.Send(context.Background(), aMessage()); err != nil {
			t.Fatal(err)
		}
	}

	// One to start with, one more after the hundredth: not 150.
	if got := m.Sessions(); got != 2 {
		t.Errorf("150 messages opened %d sessions", got)
	}
	if got := server.sessions(); got != 2 {
		t.Errorf("the server saw %d connections", got)
	}
}

func TestTheSessionSurvivesARefusalTheServerAnswered(t *testing.T) {
	// smtplib leaves the session open after a refusal because it has sent
	// RSET and the conversation is still in step. Reopening on every bounce
	// would pay a handshake per bad address.
	server := startFakeServer(t, map[string]string{"RCPT": "550 no such user"}, nil)
	m := sender(t, server)

	if err := m.Send(context.Background(), aMessage()); !errors.Is(err, mailer.ErrRejected) {
		t.Fatal(err)
	}
	server.mu.Lock()
	delete(server.refuse, "RCPT")
	server.mu.Unlock()
	if err := m.Send(context.Background(), aMessage()); err != nil {
		t.Fatal(err)
	}

	if got := m.Sessions(); got != 1 {
		t.Errorf("a refusal cost a new session (%d opened)", got)
	}
	if count := strings.Count(strings.Join(server.sent(), "\n"), "RSET"); count != 1 {
		t.Errorf("the session was left out of step: %d RSETs", count)
	}
}

func TestAnIdleSessionIsCheckedBeforeItCarriesMail(t *testing.T) {
	server := startFakeServer(t, nil, nil)
	m := sender(t, server)
	if err := m.Send(context.Background(), aMessage()); err != nil {
		t.Fatal(err)
	}
	before := strings.Count(strings.Join(server.sent(), "\n"), "NOOP")

	// Nothing waits fifteen seconds here; the session's own clock is moved
	// on, which is the seam smtp_connection.py has for the same reason.
	m.AdvanceClockForTest(20 * time.Second)
	if err := m.Send(context.Background(), aMessage()); err != nil {
		t.Fatal(err)
	}

	after := strings.Count(strings.Join(server.sent(), "\n"), "NOOP")
	if after != before+1 {
		t.Errorf("an idle session was used unchecked (%d NOOPs, was %d)", after, before)
	}
	if got := m.Sessions(); got != 1 {
		t.Errorf("a session the server had not closed was reopened (%d)", got)
	}
}

func TestASessionTheServerClosedIsReopenedRatherThanWrittenInto(t *testing.T) {
	server := startFakeServer(t, nil, nil)
	m := sender(t, server)
	if err := m.Send(context.Background(), aMessage()); err != nil {
		t.Fatal(err)
	}

	// The server will answer the next NOOP by hanging up, as a provider does
	// with an idle session.
	server.mu.Lock()
	server.refuse = map[string]string{"NOOP": "421 closing idle connection"}
	server.mu.Unlock()
	m.AdvanceClockForTest(20 * time.Second)
	if err := m.Send(context.Background(), aMessage()); err != nil {
		t.Fatal(err)
	}

	if got := m.Sessions(); got != 2 {
		t.Errorf("the closed session was used anyway (%d opened)", got)
	}
}

func TestStartTlsIsNeverSilentlySkipped(t *testing.T) {
	// The setting said the connection is to be upgraded. A server that cannot
	// is one to fix, and sending in the clear instead would be the one failure
	// nobody sees.
	server := startFakeServer(t, nil, []string{"SIZE 1000"})
	m, _, err := smtptransport.New(smtptransport.Settings{
		Host: "127.0.0.1", Port: server.port(), Security: smtptransport.StartTLS,
		From: "notes@queue.test", Timeout: 5 * time.Second, HelloName: "queue.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	err = m.Send(context.Background(), aMessage())
	if !errors.Is(err, mailer.ErrMisconfigured) {
		t.Fatalf("got %v", err)
	}
	if strings.Contains(strings.Join(server.sent(), "\n"), "MAIL FROM") {
		t.Error("the message was sent unencrypted")
	}
}

func TestTheEnvelopeCarriesTheAddressAndNotTheDisplayName(t *testing.T) {
	// MAIL FROM:<Soloclef Notes <notes@queue.test>> is refused by every real
	// server, and the refusal reads as an unverified From domain.
	server := startFakeServer(t, nil, nil)
	m := sender(t, server)
	message := aMessage()
	message.Sender = "Soloclef Nötes <notes@queue.test>"

	if err := m.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}

	conversation := strings.Join(server.sent(), "\n")
	if !strings.Contains(conversation, "MAIL FROM:<notes@queue.test>") {
		t.Errorf("the envelope sender was %q", conversation)
	}
	if !strings.Contains(conversation, "RCPT TO:<person@queue.test>") {
		t.Errorf("the envelope recipient was %q", conversation)
	}
}

func TestAnUnconfiguredTransportRefusesToStart(t *testing.T) {
	base := smtptransport.Settings{
		Host: "mail.queue.test", Port: 587, Security: smtptransport.StartTLS,
		From: "notes@queue.test", Timeout: 5 * time.Second,
	}
	refused := map[string]func(smtptransport.Settings) smtptransport.Settings{
		"no host":          func(s smtptransport.Settings) smtptransport.Settings { s.Host = ""; return s },
		"no sender":        func(s smtptransport.Settings) smtptransport.Settings { s.From = ""; return s },
		"no port":          func(s smtptransport.Settings) smtptransport.Settings { s.Port = 0; return s },
		"no timeout":       func(s smtptransport.Settings) smtptransport.Settings { s.Timeout = 0; return s },
		"unknown security": func(s smtptransport.Settings) smtptransport.Settings { s.Security = "ssl-maybe"; return s },
		"credentials in the clear": func(s smtptransport.Settings) smtptransport.Settings {
			s.Security = smtptransport.Insecure
			s.Username = "someone"
			return s
		},
	}
	for name, change := range refused {
		if _, _, err := smtptransport.New(change(base)); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}

func TestAnUnencryptedSendToSomewhereOtherThanThisMachineWarns(t *testing.T) {
	_, warning, err := smtptransport.New(smtptransport.Settings{
		Host: "relay.queue.test", Port: 25, Security: smtptransport.Insecure,
		From: "notes@queue.test", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warning, "unencrypted") {
		t.Errorf("warned %q", warning)
	}

	_, quiet, err := smtptransport.New(smtptransport.Settings{
		Host: "localhost", Port: 25, Security: smtptransport.Insecure,
		From: "notes@queue.test", Timeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if quiet != "" {
		t.Errorf("a local relay warned anyway: %q", quiet)
	}
}

func TestAMessageWithALineEndingInAHeaderIsNeverSent(t *testing.T) {
	server := startFakeServer(t, nil, nil)
	m := sender(t, server)
	message := aMessage()
	message.Subject = "Reset\r\nBcc: someone@elsewhere.test"

	err := m.Send(context.Background(), message)

	if err == nil {
		t.Fatal("it was sent")
	}
	// Not one of the port's three failures: Python builds the MIME outside its
	// try, so the queue drops it rather than retrying it.
	for _, failure := range []error{mailer.ErrUnavailable, mailer.ErrRejected, mailer.ErrMisconfigured} {
		if errors.Is(err, failure) {
			t.Errorf("a message that cannot be built reads as %v", failure)
		}
	}
	if strings.Contains(strings.Join(server.sent(), "\n"), "MAIL FROM") {
		t.Error("the conversation started anyway")
	}
}
