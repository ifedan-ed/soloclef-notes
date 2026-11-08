// Package ratelimit counts a client's requests exactly as the Python tier's
// SlowAPI limiter counts them, in the same Redis keys.
//
// app/rate_limiter.py applies 1024 requests a minute per client address per
// endpoint to every route that is not exempt. A route that moves to this tier
// without this package silently loses that cap, which is why phase 3 pairs the
// two: an attacker would otherwise prefer the moved routes.
//
// Two things make the cap real rather than decorative:
//
//   - The client is identified the same way. get_real_client_ip's order is
//     CF-Connecting-IP, then X-Real-IP, then the first entry of
//     X-Forwarded-For, then the socket peer. A tier that picked a different one
//     of those would count a different client, and a budget each tier counts on
//     its own is no budget at all.
//   - The key is the same key. The counter lives in the Redis the Python tier
//     uses, under the key the `limits` library builds, so a client's 1024 is
//     one budget across both tiers rather than 1024 on each.
package ratelimit

import (
	"fmt"
	"strings"
	"time"
)

// Limit is one of the `limits` library's rate limit items: the shape of
// "1024/minute" as that library parses it, because its parts are what the key
// and the Retry-After header are built from.
type Limit struct {
	Amount      int64
	Multiples   int64
	Granularity string
	Window      time.Duration
}

// DefaultLimit is app/rate_limiter.py's _DEFAULT_LIMIT, applied to every route
// the Python tier does not exempt.
var DefaultLimit = Limit{Amount: 1024, Multiples: 1, Granularity: "minute", Window: time.Minute}

// Message is the 429's body text, from app.py's _rate_limit_exceeded_handler.
const Message = "Too many requests. Please slow down and try again."

// String is RateLimitItem.__repr__, which is what the exception's detail is.
func (l Limit) String() string {
	return fmt.Sprintf("%d per %d %s", l.Amount, l.Multiples, l.Granularity)
}

// RetryAfter is the header value app.py sends with the 429, computed the way it
// computes it: `exc.detail.split("per")[-1].strip()`. It is a phrase rather than
// a number of seconds — that is what the Python tier sends, so it is what this
// tier sends.
func (l Limit) RetryAfter() string {
	parts := strings.Split(l.String(), "per")
	return strings.TrimSpace(parts[len(parts)-1])
}
