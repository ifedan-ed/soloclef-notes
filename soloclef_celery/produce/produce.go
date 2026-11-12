// Package produce puts a Celery task on a queue, as Celery's own Python client
// puts one there.
//
// It is the half of the protocol the API tier needs and the worker tier's beat
// already needed: the envelope internal/celery encodes, the queue
// task_routes picks, the key kombu's transport writes, and the one header this
// app adds. The Redis call itself is the Broker below — one method — because
// the two tiers reach Redis with different clients and the spelling of the
// message must not depend on which.
//
// What it does not do is declare anything. Celery's own producer hands the
// queue to kombu to declare on every publish; a message routed through the
// anonymous exchange, which is every message this app sends, is put on the
// queue's list without the binding table being read at all
// (custom/docs/go-api-tier.md, "What a Go producer has to declare").
package produce

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
	"git.danvics.com/danvics/soloclef/soloclef_celery/monotonic"
	"git.danvics.com/danvics/soloclef/soloclef_celery/queues"
	"git.danvics.com/danvics/soloclef/soloclef_celery/routing"
)

// Broker is the one Redis call a producer makes.
type Broker interface {
	// LPush is kombu's own end of the list: BRPOP's consumers read the other.
	LPush(ctx context.Context, key, value string) error
}

// Message is one task to enqueue, in the terms a .delay() or .apply_async()
// call site passes. The zero value of every option is Celery's own default, so
// a .delay(a, b) site is Message{Task: …, Args: …} and nothing else.
type Message struct {
	Task string

	// Args and Kwargs are the call's own, carried as protocol 2's body.
	Args   []any
	Kwargs map[string]any

	// ArgsAsTuple says the call site is .delay(…) rather than
	// .apply_async(args=[…]). Both exist in this app and Celery tells them
	// apart in the argsrepr header: .delay packs its arguments into a tuple.
	// Delay below sets it, so a call site reads the way the Python one does.
	ArgsAsTuple bool

	// Context is the publishing request's log context, carried into the task
	// so its log lines name the request that asked for it
	// (app/observability/logs/celery_signals.py).
	Context LogContext

	// Queue overrides task_routes, as apply_async(queue=…) does. Empty means
	// the routing table decides, which is what every call site in this app
	// leaves it to.
	Queue string

	// Countdown is apply_async(countdown=…): Celery turns it into an eta of
	// now plus this, and the worker holds the message until then.
	Countdown time.Duration

	// ExpiresIn is apply_async(expires=…) in its number-of-seconds form: an
	// expires header of now plus this, and the expiration property beside it.
	// A worker that reaches the message after it discards it rather than
	// running it late.
	//
	// Celery also takes expires as a datetime, which it turns into a number of
	// seconds against the publisher's own clock. That form is not offered
	// because nothing in this app passes one, and guessing at the subtraction
	// would put a number in the envelope that Celery did not compute.
	ExpiresIn time.Duration

	// SoftTimeLimit and HardTimeLimit are apply_async's time_limit and
	// soft_time_limit, which travel as one header.
	SoftTimeLimit time.Duration
	HardTimeLimit time.Duration

	// Retries is what Celery writes for a task being retried; a first publish
	// is 0.
	Retries int

	// Priority picks the queue's priority list. Everything this app publishes
	// is 0.
	Priority int

	// IgnoreResult says no one will read the outcome, and means no result row
	// is written at all. A task that *declares* ignore_result=True gets the
	// header whether a call site sets this or not, because Celery reads it off
	// the task object (routing.IgnoresResult).
	IgnoreResult bool

	// ParentID is the task that published this one, when one did.
	ParentID string

	// ID is apply_async(task_id=…): the id the task is published under,
	// instead of a fresh random one. It is for a task whose id is decided
	// before it is published and written somewhere the task itself checks:
	// a queued deliverable job's row names the id of the one attempt allowed
	// to claim it (app/deliverables/jobs/service.py, task_id_for_attempt),
	// and a message under any other id is ignored by the worker. Empty means
	// a fresh id, which is what every .delay() site wants.
	ID string
}

// Delay is the message a `task.delay(args…)` call site publishes.
func Delay(task string, args ...any) Message {
	return Message{Task: task, Args: args, ArgsAsTuple: true}
}

// Producer publishes for one process.
type Producer struct {
	Broker Broker
	Routes routing.Table

	// Origin and ReplyTo go into every envelope and are read by nothing here:
	// the origin names the publisher and reply_to is the RPC backend's return
	// address, which this app does not use. Both are required because an
	// envelope without them is not the envelope Celery's client writes.
	Origin  string
	ReplyTo string

	// NewID, Now and Stamp are seams for the test that compares an envelope
	// with Celery's: the ids, the clock and the enqueue stamp are the only
	// parts that differ between two publishes of the same task.
	NewID func() (string, error)
	Now   func() time.Time
	Stamp func() (time.Duration, error)
}

// Send publishes one task and returns the id Celery's client would have
// returned from .delay().
func (p Producer) Send(ctx context.Context, m Message) (string, error) {
	if err := p.validate(m); err != nil {
		return "", err
	}

	newID := p.NewID
	if newID == nil {
		newID = NewID
	}
	id := m.ID
	if id == "" {
		var err error
		if id, err = newID(); err != nil {
			return "", err
		}
	}
	tag, err := newID()
	if err != nil {
		return "", err
	}

	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	at := now()

	queue := m.Queue
	if queue == "" {
		queue = p.Routes.QueueFor(m.Task)
	}

	task := celery.Task{
		Name: m.Task,
		ID:   id,
		// A task published on its own is its own root, which is what Celery
		// writes. Nothing in this app publishes a chain (custom/docs/go-worker.md).
		RootID:        id,
		ParentID:      m.ParentID,
		Retries:       m.Retries,
		Args:          m.Args,
		Kwargs:        m.Kwargs,
		ArgsAreTuple:  m.ArgsAsTuple,
		LogContext:    m.Context.fields(),
		RoutingKey:    queue,
		Priority:      m.Priority,
		DeliveryTag:   tag,
		ReplyTo:       p.ReplyTo,
		Origin:        p.Origin,
		SoftTimeLimit: m.SoftTimeLimit,
		HardTimeLimit: m.HardTimeLimit,
		// The task's own declaration as well as the call's: Celery reads
		// `ignore_result` off the task object, so `push_send.delay(…)` carries
		// it without any call site asking (routing.IgnoresResult).
		IgnoreResult: m.IgnoreResult || routing.IgnoresResult(m.Task),
		EnqueuedAtNs: p.enqueueStamp(),
	}
	if m.Countdown > 0 {
		task.ETA = at.Add(m.Countdown)
	}
	if m.ExpiresIn > 0 {
		// Celery's send_task derives both from the same number: the header is
		// a time and the property is the window in milliseconds.
		task.Expires = at.Add(m.ExpiresIn)
		task.Expiration = m.ExpiresIn
	}

	raw, err := celery.Encode(task)
	if err != nil {
		return "", err
	}
	key := queues.Key(queue, m.Priority)
	if err := p.Broker.LPush(ctx, key, string(raw)); err != nil {
		return "", fmt.Errorf("produce: publishing %s to %s: %w", m.Task, key, err)
	}
	return id, nil
}

func (p Producer) validate(m Message) error {
	switch {
	case p.Broker == nil:
		return errors.New("produce: no broker to publish to")
	case p.Origin == "" || p.ReplyTo == "":
		return errors.New("produce: no origin or reply_to to publish under")
	case m.Task == "":
		return errors.New("produce: a published task needs a name")
	case m.Queue == "" && p.Routes.Default == "":
		return errors.New("produce: no default queue to route by")
	case m.Countdown < 0:
		return errors.New("produce: a countdown is in the past")
	case m.ExpiresIn < 0:
		return errors.New("produce: an expiry is in the past")
	}
	return nil
}

// enqueueStamp is before_task_publish's stamp: CLOCK_MONOTONIC nanoseconds,
// the same clock Python's time.monotonic_ns reads, so queue wait is measured
// the same whichever tier published. An unreadable clock leaves the header off
// rather than stamping a number from another clock, which would be reported as
// a measurement.
func (p Producer) enqueueStamp() string {
	read := monotonic.Read
	if p.Stamp != nil {
		read = p.Stamp
	}
	reading, err := read()
	if err != nil {
		return ""
	}
	return strconv.FormatInt(int64(reading), 10)
}
