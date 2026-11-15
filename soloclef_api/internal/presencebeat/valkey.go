package presencebeat

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"

	"git.danvics.com/danvics/soloclef/soloclef_notify/presence"
)

// Valkey is Marks over a Valkey server, connected on first use so a Valkey
// that is down does not stop the process serving what needs none.
type Valkey struct {
	option valkey.ClientOption
	mu     sync.Mutex
	client valkey.Client
}

// NewValkey reads the address; nothing connects yet.
func NewValkey(address string) (*Valkey, error) {
	option, err := valkey.ParseURL(address)
	if err != nil {
		return nil, fmt.Errorf("presencebeat: valkey address: %w", err)
	}
	// Nothing here reads a value back, so there is nothing to cache.
	option.DisableCache = true
	return &Valkey{option: option}, nil
}

// Mark is Marks: the client's member with its expiry as the score, the
// members that have run out swept, and the whole set's own expiry pushed
// out, in one round trip. The sweep keeps the set as small as the number of
// clients open now; the set's expiry removes it when they all go quiet.
func (v *Valkey) Mark(ctx context.Context, userID, clientID string, now time.Time) error {
	client, err := v.connection()
	if err != nil {
		return err
	}
	key := presence.Key(userID)
	for _, result := range client.DoMulti(ctx,
		client.B().Zadd().Key(key).ScoreMember().ScoreMember(presence.Expiry(now), clientID).Build(),
		client.B().Zremrangebyscore().Key(key).Min("-inf").Max(strconv.FormatInt(now.UnixMilli(), 10)).Build(),
		client.B().Pexpire().Key(key).Milliseconds(presence.TTL.Milliseconds()).Build(),
	) {
		if err := result.Error(); err != nil {
			return fmt.Errorf("presencebeat: %w", err)
		}
	}
	return nil
}

// Unmark is Marks.
func (v *Valkey) Unmark(ctx context.Context, userID, clientID string) error {
	client, err := v.connection()
	if err != nil {
		return err
	}
	if err := client.Do(ctx, client.B().Zrem().Key(presence.Key(userID)).Member(clientID).Build()).Error(); err != nil {
		return fmt.Errorf("presencebeat: %w", err)
	}
	return nil
}

func (v *Valkey) connection() (valkey.Client, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.client != nil {
		return v.client, nil
	}
	client, err := valkey.NewClient(v.option)
	if err != nil {
		return nil, fmt.Errorf("presencebeat: valkey: %w", err)
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
