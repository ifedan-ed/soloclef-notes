package livechanges

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/valkey-io/valkey-go"

	"git.danvics.com/danvics/soloclef/soloclef_notify/livechange"
)

// Valkey is this process's two ends of the bus: the publish half every
// route and background job of the API uses (Send, for livechange.Bus), and
// the listening half that feeds the hub (Listen).
//
// Connected on first use, as presence's client is: a Valkey that is down
// when the API starts must not stop it serving everything that needs none.
type Valkey struct {
	option valkey.ClientOption

	mu     sync.Mutex
	client valkey.Client
}

// NewValkey reads the address; nothing connects yet.
func NewValkey(address string) (*Valkey, error) {
	option, err := valkey.ParseURL(address)
	if err != nil {
		return nil, fmt.Errorf("livechanges: valkey address: %w", err)
	}
	// Nothing here reads a value back, so there is nothing to cache.
	option.DisableCache = true
	return &Valkey{option: option}, nil
}

// Send is livechange.Bus's one call: PUBLISH.
func (v *Valkey) Send(ctx context.Context, channel string, payload []byte) error {
	client, err := v.connection()
	if err != nil {
		return err
	}
	return client.Do(ctx, client.B().Publish().Channel(channel).Message(string(payload)).Build()).Error()
}

// The listener's retry pace after the subscription breaks: quick first, so a
// Valkey restart is a blink, and capped, so a Valkey that is gone is not
// hammered.
const (
	firstRetry = 500 * time.Millisecond
	lastRetry  = 15 * time.Second
)

// Listen subscribes to the bus and hands every message to the hub until ctx
// ends, subscribing again whenever the connection breaks.
//
// Each time the subscription is confirmed (the first time included) the hub
// is told there may have been a gap, because between a break and the new
// subscription messages were published that this process never heard: the
// hub ends every stream, and every client reconnects and reloads.
func (v *Valkey) Listen(ctx context.Context, hub *Hub, logger *slog.Logger) {
	wait := firstRetry
	for ctx.Err() == nil {
		started := time.Now()
		err := v.listenOnce(ctx, hub, logger)
		if ctx.Err() != nil {
			return
		}
		// A subscription that held for a while was healthy: start the pace
		// over rather than wait the longest after one ordinary restart.
		if time.Since(started) > time.Minute {
			wait = firstRetry
		}
		logger.WarnContext(ctx, "the live changes subscription broke; subscribing again",
			slog.String("error", fmt.Sprint(err)), slog.Duration("in", wait))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, lastRetry)
	}
}

func (v *Valkey) listenOnce(ctx context.Context, hub *Hub, logger *slog.Logger) error {
	client, err := v.connection()
	if err != nil {
		return err
	}
	dedicated, release := client.Dedicate()
	defer release()
	broken := dedicated.SetPubSubHooks(valkey.PubSubHooks{
		OnSubscription: func(s valkey.PubSubSubscription) {
			if s.Kind == "subscribe" && s.Channel == livechange.Channel {
				hub.Gap()
			}
		},
		OnMessage: func(m valkey.PubSubMessage) {
			message, err := livechange.Decode([]byte(m.Message))
			if err != nil {
				logger.Warn("a live change could not be read off the bus", slog.String("error", err.Error()))
				return
			}
			hub.Deliver(message)
		},
	})
	if err := dedicated.Do(ctx, dedicated.B().Subscribe().Channel(livechange.Channel).Build()).Error(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-broken:
		return err
	}
}

func (v *Valkey) connection() (valkey.Client, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.client != nil {
		return v.client, nil
	}
	client, err := valkey.NewClient(v.option)
	if err != nil {
		return nil, fmt.Errorf("livechanges: valkey: %w", err)
	}
	v.client = client
	return client, nil
}

// Close releases the connection.
func (v *Valkey) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.client != nil {
		v.client.Close()
		v.client = nil
	}
}
