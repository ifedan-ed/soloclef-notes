package celeryqueue

import (
	"context"
	"fmt"
	"sync"

	"github.com/valkey-io/valkey-go"
)

// ValkeyBroker is this tier's Redis client behind produce.Broker: one LPUSH,
// which is the end kombu's own producer writes.
//
// The connection is made on first use, as Celery's is: a broker that is down
// when this process starts must not stop it from serving the reads it owns,
// and a write that needs the queue reports the failure itself.
type ValkeyBroker struct {
	option valkey.ClientOption

	mutex  sync.Mutex
	client valkey.Client
}

// NewValkeyBroker reads the broker's address, which is the Python tier's
// CELERY_BROKER_URL: the same server and the same database, or the work is
// queued where no worker reads.
func NewValkeyBroker(url string) (*ValkeyBroker, error) {
	option, err := valkey.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("celery broker: %w", err)
	}
	// Nothing here reads a value back, so there is nothing to cache.
	option.DisableCache = true
	return &ValkeyBroker{option: option}, nil
}

func (b *ValkeyBroker) LPush(ctx context.Context, key, value string) error {
	client, err := b.connection()
	if err != nil {
		return err
	}
	return client.Do(ctx, client.B().Lpush().Key(key).Element(value).Build()).Error()
}

func (b *ValkeyBroker) connection() (valkey.Client, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.client != nil {
		return b.client, nil
	}
	client, err := valkey.NewClient(b.option)
	if err != nil {
		return nil, fmt.Errorf("celery broker: %w", err)
	}
	b.client = client
	return client, nil
}

// Close releases the connections.
func (b *ValkeyBroker) Close() {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if b.client != nil {
		b.client.Close()
		b.client = nil
	}
}
