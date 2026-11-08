package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer/smtp"
	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer/smtp2go"
)

// The settings are app/config's, read the same way and defaulted the same way:
// SMTP_SECURITY and the timeout have Python's defaults, and everything that
// names a server, a sender or a credential has none. An unconfigured container
// refuses to start.
type config struct {
	enabled   bool
	transport string
	senders   int
	smtp      smtp.Settings
	api       smtp2go.Settings
}

const (
	transportSMTP = "smtp"
	transportAPI  = "smtp2go_api"
)

func load() (config, error) {
	timeout, err := duration("SMTP_TIMEOUT_SECONDS", 20)
	if err != nil {
		return config{}, err
	}
	port, err := number("SMTP_PORT", 0)
	if err != nil {
		return config{}, err
	}
	cfg := config{
		enabled:   strings.EqualFold(strings.TrimSpace(os.Getenv("SMTP_ENABLED")), "true"),
		transport: strings.ToLower(strings.TrimSpace(os.Getenv("MAIL_TRANSPORT"))),
		smtp: smtp.Settings{
			Host:      strings.TrimSpace(os.Getenv("SMTP_HOST")),
			Port:      port,
			Security:  smtp.Security(security()),
			Username:  strings.TrimSpace(os.Getenv("SMTP_USERNAME")),
			Password:  os.Getenv("SMTP_PASSWORD"),
			From:      strings.TrimSpace(os.Getenv("SMTP_FROM")),
			Timeout:   timeout,
			HelloName: strings.TrimSpace(os.Getenv("SMTP_HELO_NAME")),
		},
		api: smtp2go.Settings{
			APIKey:  strings.TrimSpace(os.Getenv("SMTP2GO_API_KEY")),
			From:    strings.TrimSpace(os.Getenv("SMTP_FROM")),
			Timeout: timeout,
			BaseURL: strings.TrimSpace(os.Getenv("SMTP2GO_API_BASE_URL")),
		},
	}
	// MAIL_SENDERS has no default: how many messages leave at once is a
	// deployment's choice, not something to guess at.
	cfg.senders, err = number("MAIL_SENDERS", 0)
	if err != nil {
		return config{}, err
	}
	if cfg.senders < 1 {
		return config{}, fmt.Errorf("MAIL_SENDERS must be a whole number of at least 1 (got %q)",
			os.Getenv("MAIL_SENDERS"))
	}
	return cfg, nil
}

func security() string {
	if raw := strings.ToLower(strings.TrimSpace(os.Getenv("SMTP_SECURITY"))); raw != "" {
		return raw
	}
	return string(smtp.StartTLS)
}

// buildMailer is factory.py's: MAIL_TRANSPORT names the one way mail leaves
// and has no default. There is no fallback chain -- an unreachable or
// unconfigured mail server is an error to fix, never a reason to silently drop
// mail, or to try the other transport.
func (c config) buildMailer() (mailer.Mailer, error) {
	switch c.transport {
	case transportSMTP:
		m, warning, err := smtp.New(c.smtp)
		if err != nil {
			return nil, err
		}
		if warning != "" {
			warn(warning)
		}
		return m, nil
	case transportAPI:
		return smtp2go.New(c.api)
	default:
		return nil, fmt.Errorf("MAIL_TRANSPORT must be one of %s, %s when SMTP_ENABLED is true (got %q)",
			transportSMTP, transportAPI, c.transport)
	}
}

func number(name string, fallback int) (int, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a whole number (got %q)", name, raw)
	}
	return value, nil
}

func duration(name string, fallbackSeconds int) (time.Duration, error) {
	seconds, err := number(name, fallbackSeconds)
	if err != nil {
		return 0, err
	}
	return time.Duration(seconds) * time.Second, nil
}
