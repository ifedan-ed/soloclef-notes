package expo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_notify/pushsend"
)

// ServiceURL is Expo's push service. It is a fixed public service, like a
// provider's API host, not a deployment setting; a test points Client at a
// fake by setting BaseURL.
const ServiceURL = "https://exp.host"

const (
	sendPath     = "/--/api/v2/push/send"
	receiptsPath = "/--/api/v2/push/getReceipts"
	// maxAnswer bounds what is read of an answer: a hundred tickets or a
	// thousand receipts are a few tens of kilobytes.
	maxAnswer = 4 << 20
)

// Client is pushsend.Sender and pushsend.ReceiptReader over Expo's HTTP API.
type Client struct {
	// BaseURL is ServiceURL in production.
	BaseURL string
	// AccessToken is the Expo access token sent when the project has
	// "enhanced push security" on, or "" when it is off. It is the one
	// setting SOLOCLEF_WORKER_EXPO_ACCESS_TOKEN: absent or empty means off,
	// and Expo then refuses nothing for lack of it.
	AccessToken string
	// HTTP bounds each request by its own timeout.
	HTTP *http.Client
}

var (
	_ pushsend.Sender        = Client{}
	_ pushsend.ReceiptReader = Client{}
)

// Check reports what is missing.
func (c Client) Check() error {
	if c.BaseURL == "" || c.HTTP == nil {
		return errors.New("expo: the client has no address or no HTTP client")
	}
	return nil
}

// Send is pushsend.Sender: one request for the whole batch.
func (c Client) Send(ctx context.Context, messages []pushsend.Message) ([]pushsend.Ticket, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	if len(messages) > pushsend.MaxBatch {
		return nil, fmt.Errorf("expo: %d messages in one request; the most is %d", len(messages), pushsend.MaxBatch)
	}
	wire := make([]Message, len(messages))
	for i, m := range messages {
		// Expo has no thread or collapse id: an iPhone that is still on an
		// Expo token stacks its pushes as it always has.
		wire[i] = Message{
			To: m.To, Title: m.Title, Body: m.Body, Data: m.Data, Sound: m.Sound,
			Badge: m.Badge, ChannelID: m.ChannelID, TTL: ttlSeconds(m.TTL),
		}
	}
	var answer struct {
		Data []struct {
			Status  string `json:"status"`
			ID      string `json:"id"`
			Message string `json:"message"`
			Details struct {
				Error string `json:"error"`
			} `json:"details"`
		} `json:"data"`
	}
	if err := c.post(ctx, sendPath, wire, &answer); err != nil {
		return nil, err
	}
	// One ticket per message, in order, is the contract; an answer of
	// another length cannot be matched to the tokens and is not guessed at.
	if len(answer.Data) != len(messages) {
		return nil, fmt.Errorf("expo: %d tickets for %d messages", len(answer.Data), len(messages))
	}
	tickets := make([]pushsend.Ticket, len(messages))
	for i, data := range answer.Data {
		switch {
		case data.Status == "ok" && data.ID != "":
			tickets[i] = pushsend.Ticket{Accepted: true, ReceiptID: data.ID, Reference: data.ID}
		case data.Details.Error != "":
			tickets[i] = pushsend.Ticket{
				Error: data.Details.Error, Message: data.Message,
				Gone: data.Details.Error == DeviceNotRegistered, Retry: data.Details.Error == MessageRateExceeded,
			}
		default:
			// An error with no code is still an error, named as such so it
			// is never mistaken for an accepted message.
			tickets[i] = pushsend.Ticket{Error: "Unknown", Message: data.Message}
		}
	}
	return tickets, nil
}

// Receipts is pushsend.ReceiptReader.
func (c Client) Receipts(ctx context.Context, ids []string) (map[string]pushsend.Receipt, error) {
	if len(ids) == 0 {
		return map[string]pushsend.Receipt{}, nil
	}
	if len(ids) > pushsend.MaxReceiptBatch {
		return nil, fmt.Errorf("expo: %d receipts in one request; the most is %d", len(ids), pushsend.MaxReceiptBatch)
	}
	var answer struct {
		Data map[string]struct {
			Status  string `json:"status"`
			Message string `json:"message"`
			Details struct {
				Error string `json:"error"`
			} `json:"details"`
		} `json:"data"`
	}
	if err := c.post(ctx, receiptsPath, map[string][]string{"ids": ids}, &answer); err != nil {
		return nil, err
	}
	receipts := make(map[string]pushsend.Receipt, len(answer.Data))
	for id, data := range answer.Data {
		switch {
		case data.Status == "ok":
			receipts[id] = pushsend.Receipt{}
		case data.Details.Error != "":
			receipts[id] = pushsend.Receipt{Error: data.Details.Error, Message: data.Message, Gone: data.Details.Error == DeviceNotRegistered}
		default:
			receipts[id] = pushsend.Receipt{Error: "Unknown", Message: data.Message}
		}
	}
	return receipts, nil
}

// post sends one JSON request and reads the JSON answer. A rate limit, a
// server error or a failed connection is pushsend.ErrTransient; a refusal
// of the request itself (a 4xx) is not, because sending it again changes
// nothing.
func (c Client) post(ctx context.Context, path string, body, into any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("expo: encoding the request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("expo: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if c.AccessToken != "" {
		request.Header.Set("Authorization", "Bearer "+c.AccessToken)
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %w", pushsend.ErrTransient, err)
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxAnswer))
	if err != nil {
		return fmt.Errorf("%w: reading the answer: %w", pushsend.ErrTransient, err)
	}
	if response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500 {
		return fmt.Errorf("%w: HTTP %d: %s", pushsend.ErrTransient, response.StatusCode, clip(answer))
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("expo: HTTP %d: %s", response.StatusCode, clip(answer))
	}
	if err := json.Unmarshal(answer, into); err != nil {
		return fmt.Errorf("expo: the answer is not the push service's: %w", err)
	}
	return nil
}

// clip keeps an error body short enough for one log line. Expo's error
// bodies name codes, never a message's content.
func clip(body []byte) string {
	const most = 300
	text := strings.TrimSpace(string(body))
	if len(text) > most {
		return text[:most] + "…"
	}
	return text
}
