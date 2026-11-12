package mcptools

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"
)

// Generations is the counter that tells every process an account's MCP
// tools have changed. It lives in Valkey, where both tiers and all their
// processes can reach it.
type Generations interface {
	// Current returns the account's number. An account nobody has
	// bumped is at 0.
	Current(ctx context.Context, userID string) (int64, error)
	// Bump moves the account to a new number. Call it after the
	// transaction that changed the account's MCP connections has
	// committed, never before: a reader that saw the new number first
	// would file the old tools under it and keep them.
	Bump(ctx context.Context, userID string) error
}

// generationPrefix is the key's fixed part. The whole key is
// soloclef:mcp:generation:{user_id}, the account's uuid in lower case.
// Every writer that changes an account's MCP connections INCRs the same key
// (see the package comment), so its spelling is a contract.
const generationPrefix = "soloclef:mcp:generation:"

// GenerationKey is the Valkey key of an account's counter.
//
// The uuid is lowered here, in the one place the key is spelt: Postgres
// prints a uuid in lower case and a token may carry it in either, and a
// writer and a reader that disagreed about the case would be bumping and
// reading two different counters without either noticing.
func GenerationKey(userID string) string {
	return generationPrefix + strings.ToLower(userID)
}

// Valkey is Generations on the tier's Valkey.
//
// The connection is made on first use, like the tier's other Valkey users:
// a Valkey that is down when the process starts must not stop it serving.
// While it is down, Current fails and every turn builds its tools from
// Postgres (Source.ForTurn).
type Valkey struct {
	option valkey.ClientOption

	mu     sync.Mutex
	client valkey.Client
}

// NewValkey reads the address. It does not connect.
func NewValkey(url string) (*Valkey, error) {
	option, err := valkey.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("mcptools: valkey address: %w", err)
	}
	// The number is read once per turn and must be the server's, not a
	// copy this client kept: a client-side cache here would be the stale
	// cache the number exists to prevent.
	option.DisableCache = true
	return &Valkey{option: option}, nil
}

func (v *Valkey) connection() (valkey.Client, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.client != nil {
		return v.client, nil
	}
	client, err := valkey.NewClient(v.option)
	if err != nil {
		return nil, fmt.Errorf("mcptools: valkey: %w", err)
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

func (v *Valkey) Current(ctx context.Context, userID string) (int64, error) {
	client, err := v.connection()
	if err != nil {
		return 0, err
	}
	number, err := client.Do(ctx, client.B().Get().Key(GenerationKey(userID)).Build()).AsInt64()
	if valkey.IsValkeyNil(err) {
		return 0, nil
	}
	return number, err
}

func (v *Valkey) Bump(ctx context.Context, userID string) error {
	client, err := v.connection()
	if err != nil {
		return err
	}
	// No expiry on the key: a counter that vanished would restart at 0 and
	// could meet an entry filed under a small number long ago. The entries'
	// own lifetime bounds that too, but there is no reason to rely on it.
	return client.Do(ctx, client.B().Incr().Key(GenerationKey(userID)).Build()).Error()
}

// entry is one account's catalog and what it was built under.
type entry struct {
	catalog    *catalog
	generation int64
	expires    time.Time
}

// definitions is the per-process cache of catalogs, one per account.
//
// It holds tool definitions and nothing else. A server's address, its key,
// its trusted list and whether it still exists are read from its row when
// they are used, so nothing in here can let a removed server be called or a
// removed permission be honoured: the worst a stale entry does is offer the
// model a tool whose call then fails.
type definitions struct {
	mu      sync.Mutex
	entries map[string]entry
	// most is how many accounts are kept.
	most int
}

// get returns the account's catalog if it was built under generation and
// has not expired.
func (d *definitions) get(userID string, generation int64, now time.Time) *catalog {
	d.mu.Lock()
	defer d.mu.Unlock()
	held, found := d.entries[userID]
	if !found {
		return nil
	}
	if held.generation != generation || !now.Before(held.expires) {
		delete(d.entries, userID)
		return nil
	}
	return held.catalog
}

// put files a catalog under the generation that was read before the rows it
// was built from.
func (d *definitions) put(userID string, generation int64, built *catalog, now time.Time, lifetime time.Duration) {
	d.mu.Lock()
	defer d.mu.Unlock()
	// Two turns can build at once. The one that read the later number has
	// the later rows, so an entry is never replaced by an older one.
	if held, found := d.entries[userID]; found && held.generation > generation && now.Before(held.expires) {
		return
	}
	d.entries[userID] = entry{catalog: built, generation: generation, expires: now.Add(lifetime)}
	if len(d.entries) <= d.most {
		return
	}
	// Over the limit: first whatever has expired, then the entry nearest
	// its end. This runs only when a new account arrives at a full cache.
	var oldest string
	var oldestExpiry time.Time
	for id, held := range d.entries {
		if !now.Before(held.expires) {
			delete(d.entries, id)
			continue
		}
		if id != userID && (oldestExpiry.IsZero() || held.expires.Before(oldestExpiry)) {
			oldest, oldestExpiry = id, held.expires
		}
	}
	if len(d.entries) > d.most && !oldestExpiry.IsZero() {
		delete(d.entries, oldest)
	}
}
