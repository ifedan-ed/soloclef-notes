package smtp2go

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_mail/mailer"
)

// Settings is what a deployment must give this transport. Nothing has a
// default: an unconfigured container refuses to start rather than guessing a
// server or a sender.
type Settings struct {
	APIKey string
	// From is SMTP_FROM: the sender for a message that does not carry its own.
	From    string
	Timeout time.Duration
	// BaseURL is the API's address. Empty means smtp2go's own; a test points it
	// at a stand-in, which is the only reason it is settable.
	BaseURL string
}

// Mailer sends through POST /email/send over one kept-alive client.
type Mailer struct {
	settings Settings
	client   *http.Client
}

// New refuses what app/mailer/smtp2go_api.py's constructor refuses, at start
// rather than at the first send.
func New(settings Settings) (*Mailer, error) {
	if settings.APIKey == "" {
		return nil, errors.New("smtp2go: SMTP2GO_API_KEY is required when MAIL_TRANSPORT=smtp2go_api")
	}
	if settings.From == "" {
		return nil, errors.New("smtp2go: SMTP_FROM is required when SMTP_ENABLED is true")
	}
	if settings.Timeout <= 0 {
		return nil, errors.New("smtp2go: SMTP_TIMEOUT_SECONDS is required")
	}
	if settings.BaseURL == "" {
		settings.BaseURL = BaseURL
	}
	return &Mailer{
		settings: settings,
		// One client per mail sender, so the TLS handshake is paid once rather
		// than per message. The timeout covers the whole request, which is
		// what SMTP_TIMEOUT_SECONDS means for the SMTP transport too.
		client: &http.Client{
			Timeout:   settings.Timeout,
			Transport: &http.Transport{MaxIdleConnsPerHost: 2, IdleConnTimeout: 90 * time.Second},
		},
	}, nil
}

// Send posts one message and maps the answer onto the port's three failures.
func (m *Mailer) Send(ctx context.Context, message mailer.Message) error {
	sender := message.Sender
	if sender == "" {
		sender = m.settings.From
	}
	body := requestBody(message, sender)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		m.settings.BaseURL+Path, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("smtp2go: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Smtp2go-Api-Key", m.settings.APIKey)

	response, err := m.client.Do(request)
	if err != nil {
		// The error names the URL and sometimes the proxy, never the key or
		// the recipient, but it is not repeated into the log by the caller.
		return fmt.Errorf("%w: could not reach the smtp2go API", mailer.ErrUnavailable)
	}
	defer response.Body.Close()
	// Read it out even when it is not needed, so the connection goes back to
	// the pool rather than being closed under the next message.
	raw, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))

	return classify(response.StatusCode, raw)
}

// Close lets the kept-alive connections go.
func (m *Mailer) Close() error {
	m.client.CloseIdleConnections()
	return nil
}

// answer is the shape of the API's reply this code reads. Its free text is
// never read: smtp2go quotes the recipient's address in it.
type answer struct {
	Data struct {
		Succeeded int    `json:"succeeded"`
		Failed    int    `json:"failed"`
		ErrorCode string `json:"error_code"`
	} `json:"data"`
}

// classify is smtp2go_api.py's ladder, in its order.
//
// A 200 that reports no successful recipient is permanent, not transient: the
// provider took the request and refused the message in it, which is the same
// answer as an in-conversation 5xx over SMTP. That is the one case where "the
// request worked" and "the mail was sent" come apart, and reading only the
// status code here would report every refused message as sent.
func classify(status int, raw []byte) error {
	if status == http.StatusOK && succeeded(raw) {
		return nil
	}
	switch {
	case status == http.StatusTooManyRequests || status >= 500:
		// The provider's rate limit and its own trouble are the same answer:
		// later. retrypolicy decides how much later.
		return fmt.Errorf("%w: the smtp2go API temporarily refused the message (%d)",
			mailer.ErrUnavailable, status)
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("%w: the smtp2go API refused the key (%d); check SMTP2GO_API_KEY",
			mailer.ErrMisconfigured, status)
	default:
		return fmt.Errorf("%w: the smtp2go API refused the message (%d, %s)",
			mailer.ErrRejected, status, errorCode(raw))
	}
}

// succeeded is _succeeded(): at least one recipient taken and none failed.
// A body that is not JSON is not a success, because a 200 from something that
// is not the API is not a message that was sent.
func succeeded(raw []byte) bool {
	var reply answer
	if err := json.Unmarshal(raw, &reply); err != nil {
		return false
	}
	return reply.Data.Succeeded >= 1 && reply.Data.Failed == 0
}

// errorCode is _error_code(): smtp2go's code only, never its free text.
func errorCode(raw []byte) string {
	var reply answer
	if err := json.Unmarshal(raw, &reply); err != nil {
		return "no body"
	}
	if reply.Data.ErrorCode == "" {
		return "no code"
	}
	return reply.Data.ErrorCode
}
