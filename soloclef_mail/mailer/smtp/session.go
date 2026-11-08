package smtp

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/smtp"
	"net/textproto"
	"time"
)

// idleCheck is IDLE_CHECK_SECONDS: a session idle longer than this is checked
// with NOOP before it carries mail, because a server drops idle sessions and a
// write into a dropped one is a message that looks sent and is not.
const idleCheck = 15 * time.Second

// messagesPerSession is MESSAGES_PER_SESSION: providers cap how many messages
// one session may carry, so a fresh one is opened before the cap rather than
// after a refusal.
const messagesPerSession = 100

// session is the SMTP session one mail sender reuses. Not shared.
type session struct {
	settings Settings
	// now and dial are seams for the tests; nothing else sets them.
	now  func() time.Time
	dial func() (*smtp.Client, net.Conn, error)

	client *smtp.Client
	conn   net.Conn
	// Opened counts the sessions this connection has opened; the burst test
	// reads it, as its Python counterpart reads sessions_opened.
	Opened   int
	lastUsed time.Time
	carried  int
}

func (s *session) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// ready hands back a usable client, opening or replacing the session as
// smtp_connection.py's _ready_client does.
func (s *session) ready() (*smtp.Client, error) {
	if s.client != nil && s.carried >= messagesPerSession {
		s.close()
	}
	if s.client != nil && s.clock().Sub(s.lastUsed) > idleCheck && !s.stillOpen() {
		// A NOOP that fails means the server already closed it: this is a
		// closed connection being reopened, not a second way of sending.
		s.drop()
	}
	if s.client == nil {
		client, conn, err := s.open()
		if err != nil {
			return nil, err
		}
		s.client, s.conn = client, conn
		s.Opened++
		s.lastUsed = s.clock()
	}
	return s.client, nil
}

func (s *session) stillOpen() bool {
	if err := s.client.Noop(); err != nil {
		return false
	}
	return true
}

// open connects, secures and logs in, exactly as SMTP_SECURITY says.
func (s *session) open() (*smtp.Client, net.Conn, error) {
	client, conn, err := s.connect()
	if err != nil {
		return nil, nil, err
	}
	if err := s.secureAndLogin(client); err != nil {
		_ = client.Close()
		return nil, nil, err
	}
	return client, conn, nil
}

func (s *session) connect() (*smtp.Client, net.Conn, error) {
	if s.dial != nil {
		return s.dial()
	}
	address := s.settings.address()
	if s.settings.Security == ImplicitTLS {
		conn, err := tls.DialWithDialer(&net.Dialer{Timeout: s.settings.Timeout},
			"tcp", address, &tls.Config{ServerName: s.settings.Host})
		if err != nil {
			return nil, nil, unreachable(err)
		}
		client, err := smtp.NewClient(conn, s.settings.Host)
		if err != nil {
			_ = conn.Close()
			return nil, nil, unreachable(err)
		}
		return client, conn, nil
	}
	conn, err := net.DialTimeout("tcp", address, s.settings.Timeout)
	if err != nil {
		return nil, nil, unreachable(err)
	}
	_ = conn.SetDeadline(time.Now().Add(s.settings.Timeout))
	client, err := smtp.NewClient(conn, s.settings.Host)
	if err != nil {
		_ = conn.Close()
		return nil, nil, unreachable(err)
	}
	return client, conn, nil
}

func (s *session) secureAndLogin(client *smtp.Client) error {
	name := s.settings.HelloName
	if name != "" {
		if err := client.Hello(name); err != nil {
			return refusal(err, "EHLO")
		}
	}
	if s.settings.Security == StartTLS {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			// Never fall back to sending in the clear: the setting said the
			// connection is to be upgraded, and a server that cannot is one to
			// fix rather than to trust.
			return misconfigured("the server does not offer STARTTLS; check SMTP_PORT and SMTP_SECURITY")
		}
		if err := client.StartTLS(&tls.Config{ServerName: s.settings.Host}); err != nil {
			return refusal(err, "STARTTLS")
		}
	}
	if s.settings.Username == "" {
		return nil
	}
	auth, err := s.auth(client)
	if err != nil {
		return err
	}
	if err := client.Auth(auth); err != nil {
		return authFailure(err)
	}
	return nil
}

func (s *session) close() {
	if s.client != nil {
		// QUIT, and give up on it if the server has already gone.
		if err := s.client.Quit(); err != nil {
			_ = s.client.Close()
		}
	}
	s.drop()
}

func (s *session) drop() {
	if s.client != nil {
		_ = s.client.Close()
	}
	s.client, s.conn = nil, nil
	s.carried = 0
}

// deadline puts the timeout on the socket for one message, which is what a
// socket timeout does for smtplib on every operation. A caller whose context
// ends sooner gets the sooner of the two, so a shutdown does not wait out the
// whole timeout on a server that has stopped answering.
func (s *session) deadline(ctx context.Context) {
	if s.conn == nil {
		return
	}
	at := time.Now().Add(s.settings.Timeout)
	if sooner, ok := ctx.Deadline(); ok && sooner.Before(at) {
		at = sooner
	}
	_ = s.conn.SetDeadline(at)
}

func (s *session) sent() {
	s.lastUsed = s.clock()
	s.carried++
}

// keepAfterRefusal is the session's state after the server answered and
// refused: still in step, because the refusal was answered with RSET, so it
// stays open.
func (s *session) keepAfterRefusal() {
	if s.client != nil {
		if err := s.client.Reset(); err != nil {
			s.drop()
			return
		}
	}
	s.lastUsed = s.clock()
}

// responseCode is the SMTP code the server answered with, or 0 when the
// failure was not an answer at all.
func responseCode(err error) int {
	var protocolError *textproto.Error
	if errors.As(err, &protocolError) {
		return protocolError.Code
	}
	return 0
}
