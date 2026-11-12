package voices

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"
)

// sampleLifetime is how long a sample is kept. A voice does not change, so
// this is only a bound on how long a stale one could outlive a provider
// quietly re-recording it; a month of every voice of the largest roster is
// about a megabyte.
const sampleLifetime = 30 * 24 * time.Hour

// sampleKeyPrefix is where samples live in Redis. The "v1" is for the day
// the value's shape changes.
const sampleKeyPrefix = "soloclef:voice-sample:v1:"

// ValkeySamples keeps samples in the application's Redis: the one store
// every replica of this tier already shares. A sample is tens of kilobytes
// and there are a few dozen at most, which is well within what Redis is for.
// If Redis evicts one, it is synthesised once more.
//
// The connection is made on first use, as the rate limiter's is: a Redis
// that is down when the process starts must not stop it from serving.
type ValkeySamples struct {
	option valkey.ClientOption

	mutex  sync.Mutex
	client valkey.Client
}

// NewValkeySamples reads the store's address (the tier's REDIS_URL).
func NewValkeySamples(url string) (*ValkeySamples, error) {
	option, err := valkey.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("voice sample store: %w", err)
	}
	// Nothing here is read often enough to be worth client-side caching,
	// and without it the client works against any Redis-compatible server.
	option.DisableCache = true
	return &ValkeySamples{option: option}, nil
}

func (s *ValkeySamples) Sample(ctx context.Context, key string) ([]byte, bool, error) {
	client, err := s.connection()
	if err != nil {
		return nil, false, err
	}
	audio, err := client.Do(ctx, client.B().Get().Key(sampleKeyPrefix+key).Build()).AsBytes()
	if valkey.IsValkeyNil(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return audio, true, nil
}

func (s *ValkeySamples) Keep(ctx context.Context, key string, audio []byte) error {
	client, err := s.connection()
	if err != nil {
		return err
	}
	return client.Do(ctx, client.B().Set().Key(sampleKeyPrefix+key).
		Value(valkey.BinaryString(audio)).ExSeconds(int64(sampleLifetime.Seconds())).Build()).Error()
}

func (s *ValkeySamples) connection() (valkey.Client, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.client != nil {
		return s.client, nil
	}
	client, err := valkey.NewClient(s.option)
	if err != nil {
		return nil, fmt.Errorf("voice sample store: %w", err)
	}
	s.client = client
	return client, nil
}

// Close releases the connections.
func (s *ValkeySamples) Close() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.client != nil {
		s.client.Close()
		s.client = nil
	}
}
