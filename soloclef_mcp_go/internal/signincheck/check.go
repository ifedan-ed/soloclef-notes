// Package signincheck asks the backend whether a bearer token is a valid agent
// sign-in.
//
// The backend is the one judge of a token: it checks the signature, issuer,
// audience and expiry, then finds the account. This server only needs to know
// before a request reaches a tool, so that a bad or expired token earns an HTTP
// 401 the client can act on — refresh, or sign in again — rather than an error
// inside a tool result. It asks GET /verify-token once per token and keeps the
// answer for at most a minute, or until the token expires if that is sooner. The
// backend still checks the token on every call a tool makes.
package signincheck

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// AcceptCacheSeconds is how long a "yes" is remembered, never past the
	// token's own expiry.
	AcceptCacheSeconds = 60
	// MaxCachedTokens is how many distinct tokens are remembered at once; the
	// oldest is forgotten first.
	MaxCachedTokens = 4096
	// CheckTimeout bounds one question to the backend.
	CheckTimeout = 10 * time.Second
)

// agentMethods are the backend principal kinds this server lets through on a
// sign-in token. API keys never come here, and a web session token is not for
// agents.
var agentMethods = map[string]bool{"agent": true}

// Verdict is what the backend decided.
//
// 200 accepted; 401 invalid, and only when the backend says 401; 403 no account
// or not allowed; 503 could not be checked, meaning the backend was unreachable
// or gave any other answer, such as a 5xx while the provider is down.
type Verdict struct {
	Status   int
	Identity string
	Message  string
}

// Accepted reports whether the request may go on.
func (v Verdict) Accepted() bool { return v.Status == 200 }

// Check holds the backend address and the short memory of accepted tokens.
type Check struct {
	baseURL string
	http    *http.Client
	now     func() time.Time

	mu       sync.Mutex
	accepted map[string]acceptance
	recency  []string
}

type acceptance struct {
	until    time.Time
	identity string
}

// New builds a check against one backend.
func New(baseURL string) *Check {
	return &Check{
		baseURL:  strings.TrimRight(baseURL, "/"),
		http:     &http.Client{Timeout: CheckTimeout},
		now:      time.Now,
		accepted: map[string]acceptance{},
	}
}

// Ask returns the backend's verdict on one token.
func (c *Check) Ask(ctx context.Context, token string) Verdict {
	digest := sha256.Sum256([]byte(token))
	key := hex.EncodeToString(digest[:])
	if identity, ok := c.remembered(key); ok {
		return Verdict{Status: 200, Identity: identity}
	}

	request, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/verify-token", nil)
	if err != nil {
		return Verdict{Status: 503, Message: "Could not reach Soloclef Notes"}
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-SurfSense-Client", "mcp")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := c.http.Do(request)
	if err != nil {
		return Verdict{Status: 503, Message: "Could not reach Soloclef Notes"}
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)

	switch response.StatusCode {
	case 403:
		return Verdict{Status: 403, Message: detailOf(body)}
	case 401:
		return Verdict{Status: 401, Message: detailOf(body)}
	case 200:
	default:
		// The backend could not decide — a 5xx, or a 503 when the provider's
		// keys cannot be read. Answering 401 invalid_token here would make the
		// client drop a good token and ask the person to sign in again.
		message := detailOf(body)
		if message == "" {
			message = "Soloclef Notes could not check the sign-in; try again shortly"
		}
		return Verdict{Status: 503, Message: message}
	}

	var decoded struct {
		Method string `json:"method"`
		UserID string `json:"user_id"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return Verdict{Status: 503, Message: "Unexpected answer from Soloclef Notes"}
	}
	if !agentMethods[decoded.Method] || decoded.UserID == "" {
		return Verdict{Status: 401, Message: "Sign in through Soloclef, or use an API key"}
	}
	identity := "user:" + decoded.UserID
	c.remember(key, token, identity)
	return Verdict{Status: 200, Identity: identity}
}

func (c *Check) remembered(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.accepted[key]
	if !ok {
		return "", false
	}
	if !c.now().Before(entry.until) {
		delete(c.accepted, key)
		return "", false
	}
	return entry.identity, true
}

func (c *Check) remember(key, token, identity string) {
	keep := time.Duration(AcceptCacheSeconds) * time.Second
	if expiry, ok := unverifiedExpiry(token); ok {
		if remaining := expiry.Sub(c.now()); remaining < keep {
			keep = remaining
		}
	}
	if keep <= 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.accepted[key] = acceptance{until: c.now().Add(keep), identity: identity}
	for index, seen := range c.recency {
		if seen == key {
			c.recency = append(c.recency[:index], c.recency[index+1:]...)
			break
		}
	}
	c.recency = append(c.recency, key)
	for len(c.recency) > MaxCachedTokens {
		delete(c.accepted, c.recency[0])
		c.recency = c.recency[1:]
	}
}

// unverifiedExpiry reads the "exp" a JWT carries without checking anything; the
// backend has already checked it, and this only shortens how long a yes is kept.
func unverifiedExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Expiry *float64 `json:"exp"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil || claims.Expiry == nil {
		return time.Time{}, false
	}
	seconds, fraction := *claims.Expiry, 0.0
	fraction = seconds - float64(int64(seconds))
	return time.Unix(int64(seconds), int64(fraction*float64(time.Second))), true
}

func detailOf(body []byte) string {
	var decoded struct {
		Detail any `json:"detail"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		return ""
	}
	if text, ok := decoded.Detail.(string); ok {
		return text
	}
	return ""
}
