package smtp

import (
	"errors"
	"net/smtp"
)

// auth picks the mechanism smtplib's login would have picked, out of the two
// Go can speak.
//
// smtplib prefers CRAM-MD5, then PLAIN, then LOGIN. CRAM-MD5 is not here: the
// standard library does not implement it, and writing it would be writing a
// keyed digest by hand for a mechanism no provider this app uses still
// advertises. So PLAIN when it is offered, LOGIN when it is not, and a refusal
// to guess when neither is -- never an unauthenticated send, which the server
// would answer with "relay denied" anyway.
func (s *session) auth(client *smtp.Client) (smtp.Auth, error) {
	ok, mechanisms := client.Extension("AUTH")
	if !ok {
		return nil, misconfigured("the server offers no AUTH but SMTP_USERNAME is set")
	}
	switch {
	case contains(mechanisms, "PLAIN"):
		return smtp.PlainAuth("", s.settings.Username, s.settings.Password, s.settings.Host), nil
	case contains(mechanisms, "LOGIN"):
		return &loginAuth{username: s.settings.Username, password: s.settings.Password}, nil
	default:
		return nil, misconfigured("the server offers no AUTH mechanism this transport speaks: " + mechanisms)
	}
}

func contains(mechanisms, name string) bool {
	for _, field := range splitFields(mechanisms) {
		if field == name {
			return true
		}
	}
	return false
}

func splitFields(s string) []string {
	var fields []string
	start := -1
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == ' ' {
			if start >= 0 {
				fields = append(fields, s[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
	}
	return fields
}

// loginAuth is AUTH LOGIN, which has no RFC and is what several providers
// offer instead of PLAIN: the username and then the password, each base64, in
// answer to a prompt whose wording is not fixed.
type loginAuth struct {
	username string
	password string
	sent     int
}

func (a *loginAuth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	if !server.TLS {
		// The standard library's PlainAuth refuses this too. Credentials do
		// not go out in the clear, and SMTP_SECURITY=none already refuses a
		// username, so reaching here means the upgrade did not happen.
		return "", nil, errors.New("smtp: refusing to send credentials over an unencrypted connection")
	}
	return "LOGIN", nil, nil
}

func (a *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	a.sent++
	switch a.sent {
	case 1:
		return []byte(a.username), nil
	case 2:
		return []byte(a.password), nil
	default:
		return nil, errors.New("smtp: the server asked for more than a username and a password")
	}
}
