// Package smtp sends over SMTP (MAIL_TRANSPORT=smtp), which is the transport
// Soloclef has been delivering through: smtp2go's SMTP endpoint on 587 with
// STARTTLS.
//
// app/mailer/smtp.py and app/mailer/smtp_connection.py are what this mirrors,
// including the parts that look like caution for its own sake: the security
// mode comes from one setting and nothing tries a second mode, a session idle
// for a while is checked before it carries mail, and a session is replaced
// after a hundred messages because providers cap them.
package smtp

import (
	"errors"
	"fmt"
	"time"
)

// Security is SMTP_SECURITY. It is the whole of the decision: TLS is implicit
// TLS from the first byte (465), STARTTLS upgrades a plain connect (587), and
// nothing tries one and then the other. Inferring it from the port number is
// the classic source of "it hangs forever with no error".
type Security string

const (
	ImplicitTLS Security = "tls"
	StartTLS    Security = "starttls"
	Insecure    Security = "none"
)

// Settings is what a deployment gives this transport. Nothing has a default
// that names a server.
type Settings struct {
	Host     string
	Port     int
	Security Security
	Username string
	Password string
	// From is SMTP_FROM, for a message that carries no sender of its own.
	From    string
	Timeout time.Duration
	// HelloName is the name sent in EHLO. smtplib uses socket.getfqdn(); this
	// is settable because a container's idea of its own name is rarely the one
	// a provider should see, and an empty one would be a bare EHLO.
	HelloName string
}

var loopbackHosts = map[string]bool{
	"localhost": true, "127.0.0.1": true, "::1": true, "[::1]": true,
}

// Validate refuses what SmtpMailer's constructor refuses, at start rather than
// at the first send, and returns the warning it logs.
func (s Settings) Validate() (warning string, err error) {
	if s.Host == "" {
		return "", errors.New("smtp: SMTP_HOST is required when SMTP_ENABLED is true")
	}
	// Mandatory even though a message may override it: a deployment with no
	// From at all should fail at startup, not at the first send.
	if s.From == "" {
		return "", errors.New("smtp: SMTP_FROM is required when SMTP_ENABLED is true")
	}
	if s.Port <= 0 {
		return "", errors.New("smtp: SMTP_PORT is required when SMTP_ENABLED is true")
	}
	if s.Timeout <= 0 {
		return "", errors.New("smtp: SMTP_TIMEOUT_SECONDS is required when SMTP_ENABLED is true")
	}
	switch s.Security {
	case ImplicitTLS, StartTLS:
	case Insecure:
		if s.Username != "" {
			return "", errors.New("smtp: SMTP_SECURITY=none refuses to send credentials in the clear. " +
				"Use starttls or tls, or drop the username for a local relay")
		}
		if !loopbackHosts[s.Host] {
			return fmt.Sprintf("SMTP_SECURITY=none to non-loopback host %s: mail will cross "+
				"the network unencrypted.", s.Host), nil
		}
	default:
		return "", fmt.Errorf("smtp: unknown SMTP_SECURITY %q. Expected one of: starttls, tls, none", s.Security)
	}
	return "", nil
}

func (s Settings) address() string {
	return fmt.Sprintf("%s:%d", s.Host, s.Port)
}
