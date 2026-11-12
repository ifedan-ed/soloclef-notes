package eventstream

import (
	"context"
	"fmt"
	"strconv"
	"sync"

	"github.com/valkey-io/valkey-go"
)

// Publisher appends events to the stream the dispatcher reads.
//
// The connection is made on first use, as ratelimit.ValkeyCounter's is: a Redis
// that is down when this process starts must not stop it from serving.
type Publisher struct {
	option valkey.ClientOption

	mutex  sync.Mutex
	client valkey.Client
}

// NewPublisher reads the stream's address. The URL is the Python tier's
// REDIS_APP_URL — the same server and the same database, or the dispatcher is
// reading a stream nothing writes to.
func NewPublisher(url string) (*Publisher, error) {
	option, err := valkey.ParseURL(url)
	if err != nil {
		return nil, fmt.Errorf("event stream redis: %w", err)
	}
	// Nothing here reads a value back, so there is nothing to cache.
	option.DisableCache = true
	return &Publisher{option: option}, nil
}

// Publish appends one event and returns the entry id the stream gave it.
//
// Call it after the transaction has committed, for the reason
// app/event_bus/publish.py gives: an event is a fact, and a transaction that
// rolled back has no fact to report. database.Effects is where that belongs.
func (p *Publisher) Publish(ctx context.Context, event Event) (string, error) {
	fields, err := event.Encode()
	if err != nil {
		return "", err
	}
	client, err := p.connection()
	if err != nil {
		return "", err
	}
	command := client.B().Xadd().Key(Stream).
		Maxlen().Almost().Threshold(strconv.Itoa(RetentionEntries)).
		Id("*").FieldValue().FieldValue(Field, fields[Field]).Build()
	id, err := client.Do(ctx, command).ToString()
	if err != nil {
		return "", fmt.Errorf("publishing %s: %w", event.EventType, err)
	}
	return id, nil
}

func (p *Publisher) connection() (valkey.Client, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.client != nil {
		return p.client, nil
	}
	client, err := valkey.NewClient(p.option)
	if err != nil {
		return nil, fmt.Errorf("event stream redis: %w", err)
	}
	p.client = client
	return client, nil
}

// Close releases the connection.
func (p *Publisher) Close() {
	p.mutex.Lock()
	defer p.mutex.Unlock()
	if p.client != nil {
		p.client.Close()
		p.client = nil
	}
}
