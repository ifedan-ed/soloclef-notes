// Package apns pushes iPhones straight through Apple's push service (APNs),
// over HTTP/2 with token authentication: an ES256 JWT signed with the
// team's .p8 key, kept for fifty minutes (Signer).
//
// Before this, iPhones were pushed through Expo's service, which relayed to
// APNs with a key Expo held. Sending to Apple directly takes a third party
// out of the path (Expo no longer sees who is pushed, or when) and a hop out
// of every push.
//
// Client is pushsend.Sender. APNs answers each push at once, in its own
// HTTP/2 stream, so there are no receipts to read later: a token that is
// gone is known from the answer itself and deleted then.
//
// One Client pushes one environment: production (App Store and TestFlight
// builds) or sandbox (development builds). A device token is minted for one
// of them and Apple refuses it in the other with BadDeviceToken, so the
// delivery job never offers this client a token of the other environment
// (push_devices.apns_environment); a BadDeviceToken it does get means the
// token is bad, and it is deleted.
package apns

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_notify/pushsend"
)

// The environments, as SOLOCLEF_WORKER_APNS_ENVIRONMENT and
// push_devices.apns_environment spell them.
const (
	Production = "production"
	Sandbox    = "sandbox"
)

// Apple's two hosts. They are fixed public services, not deployment
// settings: the environment picks one, and a test points BaseURL at a fake.
const (
	ProductionURL = "https://api.push.apple.com"
	SandboxURL    = "https://api.sandbox.push.apple.com"
)

// URLFor is the host of an environment.
func URLFor(environment string) (string, error) {
	switch environment {
	case Production:
		return ProductionURL, nil
	case Sandbox:
		return SandboxURL, nil
	}
	return "", fmt.Errorf("apns: the environment is %q; it is %q or %q", environment, Production, Sandbox)
}

const (
	// maxPayload is Apple's limit for an alert's JSON.
	maxPayload = 4096
	// maxCollapseID is Apple's limit for apns-collapse-id, in bytes.
	maxCollapseID = 64
	// streams is how many pushes of one batch are in flight at once, each
	// its own stream of one HTTP/2 connection. Apple allows far more
	// streams than this on a connection; twenty keeps a batch of a hundred
	// to a few round trips without one job holding the connection.
	streams = 20
	// requestTimeout bounds one push. APNs answers in tens of
	// milliseconds; ten seconds is a connection that is not coming back,
	// and the push is tried again later from the queue.
	requestTimeout = 10 * time.Second
	// maxAnswer bounds what is read of an answer: {"reason": "..."}.
	maxAnswer = 4 << 10
)

// Apple's reasons that the client turns into a decision.
const (
	reasonBadDeviceToken       = "BadDeviceToken"
	reasonUnregistered         = "Unregistered"
	reasonExpiredToken         = "ExpiredToken"
	reasonExpiredProviderToken = "ExpiredProviderToken"
)

// deviceToken is what an APNs device token looks like: 32 to 100 bytes in
// lower-case hex, as the phone app sends it and
// soloclef_api/internal/pushdevices stores it. A token is also a path
// segment here, so nothing else may reach the URL.
var deviceToken = regexp.MustCompile(`^(?:[0-9a-f]{2}){32,100}$`)

// Client is pushsend.Sender over APNs.
type Client struct {
	// BaseURL is URLFor(Environment) in production.
	BaseURL string
	// Topic is the app's bundle id (such as com.example.notes): Apple delivers a
	// push only to the app it names, and the key may serve every app of the
	// team.
	Topic string
	// Signer makes the provider token.
	Signer *Signer
	// HTTP is NewHTTPClient's: HTTP/2 only, each request bounded.
	HTTP *http.Client
	Log  *slog.Logger
	// Now is nil for the system clock; only a test names another.
	Now func() time.Time
}

var _ pushsend.Sender = (*Client)(nil)

// NewHTTPClient is the HTTP client APNs is spoken to with: HTTP/2 only, as
// Apple requires, from Go's own net/http (no other library), with the
// connection kept between pushes so a push costs one round trip and not a
// TLS handshake. Pings find a connection Apple dropped silently before a
// push waits on it. roots is nil for the system's certificate authorities;
// a test passes its fake's.
func NewHTTPClient(roots *x509.CertPool) *http.Client {
	protocols := new(http.Protocols)
	protocols.SetHTTP2(true)
	return &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			TLSClientConfig:     &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots},
			Protocols:           protocols,
			ForceAttemptHTTP2:   true,
			TLSHandshakeTimeout: requestTimeout,
			// Pushes come in bursts with quiet between: half an hour idle
			// keeps the connection through a quiet spell without holding it
			// open all night.
			IdleConnTimeout: 30 * time.Minute,
			HTTP2: &http.HTTP2Config{
				SendPingTimeout: time.Minute,
				PingTimeout:     15 * time.Second,
			},
		},
	}
}

// Check reports what is missing.
func (c *Client) Check() error {
	switch {
	case c.BaseURL == "" || c.HTTP == nil || c.Signer == nil || c.Log == nil:
		return errors.New("apns: the client has no address, HTTP client, signer or log")
	case c.Topic == "":
		return errors.New("apns: the client has no topic (the app's bundle id)")
	}
	return nil
}

func (c *Client) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

// Send is pushsend.Sender: each message is its own request, up to streams
// of them at once, and each answers in its own ticket. The call itself
// fails only for a batch over the limit.
func (c *Client) Send(ctx context.Context, messages []pushsend.Message) ([]pushsend.Ticket, error) {
	if len(messages) > pushsend.MaxBatch {
		return nil, fmt.Errorf("apns: %d messages in one call; the most is %d", len(messages), pushsend.MaxBatch)
	}
	tickets := make([]pushsend.Ticket, len(messages))
	// A refusal of the server's own credentials or topic is the same for
	// every push of the batch: it is logged once, as the error it is.
	var faultOnce sync.Once
	fault := func(ticket pushsend.Ticket) {
		faultOnce.Do(func() {
			c.Log.Error("Apple refused this server's push credentials or topic; check SOLOCLEF_WORKER_APNS_*",
				"reason", ticket.Error, "apns_id", ticket.Reference, "topic", c.Topic)
		})
	}
	slots := make(chan struct{}, streams)
	var wg sync.WaitGroup
	for i, message := range messages {
		select {
		case slots <- struct{}{}:
		case <-ctx.Done():
			// The job's time is up: what was not sent is tried again later.
			tickets[i] = pushsend.Ticket{Retry: true, Error: "NotSent", Message: ctx.Err().Error()}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			tickets[i] = c.push(ctx, message)
			if isFault(tickets[i].Error) {
				fault(tickets[i])
			}
		}()
	}
	wg.Wait()
	return tickets, nil
}

// push sends one message, renewing the provider token once if Apple says
// it has expired.
func (c *Client) push(ctx context.Context, message pushsend.Message) pushsend.Ticket {
	if !deviceToken.MatchString(message.To) {
		// Never valid, so never sent: the API takes only hex tokens, and a
		// row that is not one can never be pushed.
		return pushsend.Ticket{Gone: true, Error: reasonBadDeviceToken, Message: "the token is not an APNs device token"}
	}
	body, err := encode(message)
	if err != nil {
		return pushsend.Ticket{Error: "PayloadTooLarge", Message: err.Error()}
	}
	// The id is this server's, made before sending, so a push that never
	// got an answer can still be named in the log.
	id := newID()
	for attempt := 0; ; attempt++ {
		token, err := c.Signer.Token(c.now())
		if err != nil {
			return pushsend.Ticket{Error: "ProviderToken", Message: err.Error(), Reference: id}
		}
		status, reason, err := c.post(ctx, message, body, token, id)
		if err != nil {
			return pushsend.Ticket{Retry: true, Error: "Unreachable", Message: err.Error(), Reference: id}
		}
		if status == http.StatusForbidden && reason == reasonExpiredProviderToken && attempt == 0 {
			// Apple's clock and ours disagree about the token's age: a new
			// one, once, then this push again at once (the refusal was
			// about this server, not the phone).
			c.Signer.Renew(token)
			c.Log.Info("Apple said the provider token expired; signed a new one", "apns_id", id)
			continue
		}
		ticket := classify(status, reason)
		ticket.Reference = id
		return ticket
	}
}

// post is one request: the push to the device, with Apple's headers. It
// answers the status and Apple's reason ("" on success).
func (c *Client) post(ctx context.Context, message pushsend.Message, body []byte, token, id string) (int, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/3/device/"+message.To, bytes.NewReader(body))
	if err != nil {
		return 0, "", err
	}
	header := request.Header
	header.Set("authorization", "bearer "+token)
	header.Set("content-type", "application/json")
	header.Set("apns-id", id)
	header.Set("apns-topic", c.Topic)
	// An alert the person sees, sent at once: Apple requires the push type
	// to match the payload, and priority 10 is "deliver now" (5 lets the
	// phone wait for a convenient moment, which a finished video is not).
	header.Set("apns-push-type", "alert")
	header.Set("apns-priority", "10")
	if message.TTL > 0 {
		header.Set("apns-expiration", strconv.FormatInt(c.now().Add(message.TTL).Unix(), 10))
	}
	if message.CollapseID != "" {
		header.Set("apns-collapse-id", collapseID(message.CollapseID))
	}
	response, err := c.HTTP.Do(request)
	if err != nil {
		return 0, "", err
	}
	defer response.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(response.Body, maxAnswer))
	if err != nil {
		return 0, "", fmt.Errorf("reading Apple's answer: %w", err)
	}
	if response.StatusCode == http.StatusOK {
		return http.StatusOK, "", nil
	}
	var refusal struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(answer, &refusal) != nil || refusal.Reason == "" {
		refusal.Reason = "HTTP " + strconv.Itoa(response.StatusCode)
	}
	return response.StatusCode, refusal.Reason, nil
}

// classify is Apple's answer as a decision (Apple's "Handling notification
// responses from APNs" lists the statuses and reasons):
//
//   - 200: accepted.
//   - 410 (Unregistered, ExpiredToken), or 400 BadDeviceToken: the token
//     will never work again; it is deleted. The delivery job sends a token
//     only to the environment it was minted in, so BadDeviceToken is a bad
//     token here and not an environment mismatch.
//   - 429 (too many pushes to this phone) and 5xx (Apple's own trouble):
//     may pass; tried again later from the queue.
//   - Anything else (a 403 about this server's key, a payload Apple
//     refuses, DeviceTokenNotForTopic): logged, and the token is kept. A
//     wrong topic or key would answer the same for every phone, and
//     deleting every token for it would sign every iPhone out of pushes.
func classify(status int, reason string) pushsend.Ticket {
	switch {
	case status == http.StatusOK:
		return pushsend.Ticket{Accepted: true}
	case status == http.StatusGone, status == http.StatusBadRequest && reason == reasonBadDeviceToken:
		return pushsend.Ticket{Gone: true, Error: reason}
	case status == http.StatusTooManyRequests, status >= 500:
		return pushsend.Ticket{Retry: true, Error: reason}
	}
	return pushsend.Ticket{Error: reason, Message: "HTTP " + strconv.Itoa(status)}
}

// isFault is a refusal of this server's own setup rather than of one push.
func isFault(reason string) bool {
	switch reason {
	case "InvalidProviderToken", "MissingProviderToken", "BadCertificate", "BadCertificateEnvironment",
		"BadTopic", "MissingTopic", "TopicDisallowed", "DeviceTokenNotForTopic", "ProviderToken":
		return true
	}
	return false
}

// alert, aps and payload are the JSON Apple shows: the alert, the badge,
// the sound and the thread in "aps".
type alert struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

type aps struct {
	Alert    alert  `json:"alert"`
	Badge    *int   `json:"badge,omitempty"`
	Sound    string `json:"sound,omitempty"`
	ThreadID string `json:"thread-id,omitempty"`
}

// payload carries the app's data under "body": on iOS, expo-notifications
// hands a remote push's userInfo["body"] to the app as content.data, which
// is where the app reads kind, path, v and server on a tap
// (soloclef_mobile src/lib/push). It is the key Expo's own service put
// them under, so a push from here and one relayed by Expo read the same.
type payload struct {
	APS  aps            `json:"aps"`
	Body map[string]any `json:"body,omitempty"`
}

// encode is the message as Apple's JSON, refused when over Apple's limit
// rather than sent to be refused.
func encode(message pushsend.Message) ([]byte, error) {
	body, err := json.Marshal(payload{
		APS: aps{
			Alert: alert{Title: message.Title, Body: message.Body},
			Badge: message.Badge, Sound: message.Sound, ThreadID: message.ThreadID,
		},
		Body: message.Data,
	})
	if err != nil {
		return nil, fmt.Errorf("encoding the push: %w", err)
	}
	if len(body) > maxPayload {
		return nil, fmt.Errorf("the push is %d bytes; Apple takes %d", len(body), maxPayload)
	}
	return body, nil
}

// collapseID is the id as Apple takes it: as it is when it fits in 64
// bytes, otherwise its SHA-256 in hex (exactly 64), so two keys that are
// different stay different and the same key always collapses the same.
func collapseID(key string) string {
	if len(key) <= maxCollapseID {
		return key
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// newID is a random (version 4) UUID, the form Apple takes for apns-id.
func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}
