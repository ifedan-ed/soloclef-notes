package nextcloud

import (
	"context"
	"time"
)

// SetSleep replaces how a client waits between attempts, so a test of
// retrying does not wait out the real backoff.
func (c *Client) SetSleep(sleep func(context.Context, time.Duration) error) { c.sleep = sleep }
