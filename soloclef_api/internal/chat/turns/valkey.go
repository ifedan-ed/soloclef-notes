package turns

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"
)

// The keys and the channel. The thread goes in braces so that, on a cluster,
// a thread's lease and its stop flag hash to one slot and the scripts below
// may touch both.
const (
	leasePrefix = "soloclef:chat:turn:{"
	stopPrefix  = "soloclef:chat:stop:{"
	keySuffix   = "}"
	stopChannel = "soloclef:chat:stop"
)

// Each decision is one script so that it is one step on the server: "renew
// if it is still mine" done as a read and then a write would let a lease
// that expired in between be renewed over its new owner.
const (
	// KEYS: lease, stop. ARGV: owner, lifetime ms. Returns 1 if claimed.
	claimScript = `
if redis.call("set", KEYS[1], ARGV[1], "NX", "PX", ARGV[2]) then
    redis.call("del", KEYS[2])
    return 1
end
return 0`

	// Returns 0 lost, 1 held, 2 stop requested.
	renewScript = `
if redis.call("get", KEYS[1]) ~= ARGV[1] then
    return 0
end
redis.call("pexpire", KEYS[1], ARGV[2])
if redis.call("exists", KEYS[2]) == 1 then
    return 2
end
return 1`

	releaseScript = `
if redis.call("get", KEYS[1]) == ARGV[1] then
    redis.call("del", KEYS[1], KEYS[2])
end
return 1`

	// ARGV: lifetime ms of the flag, channel, thread. Returns 1 if a turn
	// was running. The flag is given the lease's remaining life at most, so
	// it cannot outlive the turn it was meant for.
	stopScript = `
local ttl = redis.call("pttl", KEYS[1])
if ttl <= 0 then
    return 0
end
redis.call("set", KEYS[2], "1", "PX", ttl)
redis.call("publish", ARGV[1], ARGV[2])
return 1`
)

// Valkey is the Coordinator every process of the tier shares.
//
// The connection is made on first use, like the tier's other Valkey users:
// a Valkey that is down when the process starts must not stop it serving
// the routes that need none.
type Valkey struct {
	option valkey.ClientOption

	claim, renew, release, stop *valkey.Lua

	mu     sync.Mutex
	client valkey.Client
}

// NewValkey reads the address. It does not connect.
func NewValkey(url string) (*Valkey, error) {
	option, err := valkey.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("turns: valkey address: %w", err)
	}
	// Nothing here is read twice, so there is nothing to cache client-side.
	option.DisableCache = true
	return &Valkey{
		option:  option,
		claim:   valkey.NewLuaScript(claimScript),
		renew:   valkey.NewLuaScript(renewScript),
		release: valkey.NewLuaScript(releaseScript),
		stop:    valkey.NewLuaScript(stopScript),
	}, nil
}

func (v *Valkey) connection() (valkey.Client, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.client != nil {
		return v.client, nil
	}
	client, err := valkey.NewClient(v.option)
	if err != nil {
		return nil, fmt.Errorf("turns: valkey: %w", err)
	}
	v.client = client
	return client, nil
}

// Close releases the connections.
func (v *Valkey) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.client != nil {
		v.client.Close()
		v.client = nil
	}
}

func keys(thread string) []string {
	return []string{leasePrefix + thread + keySuffix, stopPrefix + thread + keySuffix}
}

func millis(d time.Duration) string {
	return strconv.FormatInt(d.Milliseconds(), 10)
}

func (v *Valkey) Claim(ctx context.Context, thread, owner string, lifetime time.Duration) (bool, error) {
	client, err := v.connection()
	if err != nil {
		return false, err
	}
	n, err := v.claim.Exec(ctx, client, keys(thread), []string{owner, millis(lifetime)}).AsInt64()
	return n == 1, err
}

func (v *Valkey) Renew(ctx context.Context, thread, owner string, lifetime time.Duration) (Standing, error) {
	client, err := v.connection()
	if err != nil {
		return 0, err
	}
	n, err := v.renew.Exec(ctx, client, keys(thread), []string{owner, millis(lifetime)}).AsInt64()
	if err != nil {
		return 0, err
	}
	switch n {
	case 1:
		return Held, nil
	case 2:
		return StopRequested, nil
	default:
		return Lost, nil
	}
}

func (v *Valkey) Release(ctx context.Context, thread, owner string) error {
	client, err := v.connection()
	if err != nil {
		return err
	}
	return v.release.Exec(ctx, client, keys(thread), []string{owner}).Error()
}

func (v *Valkey) RequestStop(ctx context.Context, thread string) (bool, error) {
	client, err := v.connection()
	if err != nil {
		return false, err
	}
	n, err := v.stop.Exec(ctx, client, keys(thread), []string{stopChannel, thread}).AsInt64()
	return n == 1, err
}

func (v *Valkey) Running(ctx context.Context, thread string) (bool, error) {
	client, err := v.connection()
	if err != nil {
		return false, err
	}
	n, err := client.Do(ctx, client.B().Exists().Key(keys(thread)[0]).Build()).AsInt64()
	return n == 1, err
}

// Listen subscribes on a connection of its own (a subscribed connection can
// do nothing else) and returns when ctx ends or the subscription breaks.
func (v *Valkey) Listen(ctx context.Context, deliver func(string)) error {
	client, err := v.connection()
	if err != nil {
		return err
	}
	err = client.Receive(ctx, client.B().Subscribe().Channel(stopChannel).Build(), func(msg valkey.PubSubMessage) {
		deliver(msg.Message)
	})
	if ctx.Err() != nil {
		return nil
	}
	return err
}
