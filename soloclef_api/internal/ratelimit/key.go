package ratelimit

import "fmt"

// storagePrefix is limits.storage.RedisStorage.PREFIX, which it puts in front of
// every key it writes.
const storagePrefix = "LIMITS"

// namespace is limits.RateLimitItem's default namespace.
const namespace = "LIMITER"

// Key is the Redis key the Python tier's counter lives in, so the two tiers
// share one budget per client.
//
// It is RateLimitItem.key_for(client, endpoint) behind RedisStorage's
// prefixed_key: the identifiers the limiter passes are the client key and the
// limit's scope, which for a default limit is the request's own path — not the
// route template, so each workspace id is counted separately, exactly as there.
func Key(limit Limit, clientIP, path string) string {
	return fmt.Sprintf("%s:%s/%s/%s/%d/%d/%s",
		storagePrefix, namespace, clientIP, path, limit.Amount, limit.Multiples, limit.Granularity)
}
