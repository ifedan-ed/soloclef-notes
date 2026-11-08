package ratelimit

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"
)

// incrementAndExpire is the Lua script the `limits` library runs, byte for byte
// (limits/resources/redis/lua_scripts/incr_expire.lua). The window starts at the
// first hit rather than on a clock boundary, which is why the expiry is only set
// when the counter is at exactly the cost of this hit.
const incrementAndExpire = `local current
local amount = tonumber(ARGV[2])
current = redis.call("incrby", KEYS[1], amount)

if tonumber(current) == amount then
    redis.call("expire", KEYS[1], ARGV[1])
end

return current
`

// ValkeyCounter counts in the Redis the Python tier counts in.
//
// The connection is made on first use rather than at startup, as redis-py's is:
// a Redis that is down when this process starts must not stop it from serving,
// because the limiter already has an answer for a counter it cannot reach.
type ValkeyCounter struct {
	option valkey.ClientOption
	script *valkey.Lua

	mutex  sync.Mutex
	client valkey.Client
}

// NewValkeyCounter reads the counter's address. The URL is the Python tier's
// REDIS_APP_URL: the same server and the same database, or the budgets are two.
func NewValkeyCounter(url string) (*ValkeyCounter, error) {
	option, err := valkey.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("rate limit redis: %w", err)
	}
	// Nothing here reads a value back, so there is nothing to cache, and
	// client-side caching would need the server to support tracking.
	option.DisableCache = true
	return &ValkeyCounter{option: option, script: valkey.NewLuaScript(incrementAndExpire)}, nil
}

// Hit adds cost to the window and returns the count after it, as
// RedisStorage.incr does.
func (c *ValkeyCounter) Hit(ctx context.Context, key string, window time.Duration, cost int64) (int64, error) {
	client, err := c.connection()
	if err != nil {
		return 0, err
	}
	result := c.script.Exec(ctx, client,
		[]string{key},
		[]string{strconv.FormatInt(int64(window.Seconds()), 10), strconv.FormatInt(cost, 10)})
	count, err := result.AsInt64()
	if err != nil {
		return 0, fmt.Errorf("counting the request: %w", err)
	}
	return count, nil
}

// Reachable is RedisStorage.check: whether the counter can be used again after
// it has been found dead.
func (c *ValkeyCounter) Reachable(ctx context.Context) error {
	client, err := c.connection()
	if err != nil {
		return err
	}
	return client.Do(ctx, client.B().Ping().Build()).Error()
}

func (c *ValkeyCounter) connection() (valkey.Client, error) {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.client != nil {
		return c.client, nil
	}
	client, err := valkey.NewClient(c.option)
	if err != nil {
		return nil, fmt.Errorf("rate limit redis: %w", err)
	}
	c.client = client
	return client, nil
}

// Close releases the connections.
func (c *ValkeyCounter) Close() {
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}
}
