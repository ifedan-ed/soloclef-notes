package celery

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Task is one message to put on a queue, in the terms Celery's own client
// publishes in. Only the scheduler publishes from here — "only the consumer
// moves" still holds for everything the FastAPI app enqueues — so what this
// writes has to be indistinguishable from what app/celery_app.py writes, down
// to the headers nothing reads.
type Task struct {
	Name string
	ID   string

	// RootID is the head of the chain this task belongs to. A task published
	// on its own is its own root, which is what Celery writes.
	RootID   string
	ParentID string
	Retries  int

	Args   []any
	Kwargs map[string]any

	// ArgsAreTuple says the call site was .delay(…) rather than
	// .apply_async(args=[…]). Celery reprs the arguments exactly as it was
	// given them, and .delay packs them into a tuple — so the argsrepr header
	// is "(7,)" for one and "[7]" for the other. Nothing reads that header but
	// a human and Flower; it is reproduced because the envelope is compared
	// byte for byte.
	ArgsAreTuple bool

	// RoutingKey is the queue. Celery publishes a routed task through the
	// anonymous exchange with the queue as the routing key, so Exchange is
	// empty for everything this app sends.
	RoutingKey string
	Exchange   string
	Priority   int

	// DeliveryTag is the key of the unacked registry that stands in for a
	// broker ack on Redis, so a message published without one could never be
	// acknowledged.
	DeliveryTag string

	// ReplyTo is the publisher's oid. The RPC result backend replies there;
	// this app's backend is Redis, so it is written and never read.
	ReplyTo string

	// Origin is who published, as Celery writes it: gen<pid>@<hostname>.
	Origin string

	ETA     time.Time
	Expires time.Time

	// Expiration is the per-message TTL Celery records beside expires: the
	// same window in milliseconds, as a string, which is AMQP's own
	// expiration property. kombu's Redis transport does nothing with it, and
	// it is written because Celery writes it.
	Expiration time.Duration

	SoftTimeLimit time.Duration
	HardTimeLimit time.Duration

	IgnoreResult bool

	// LogContext is this app's own soloclef_log_context header
	// (app/observability/logs/celery_signals.py): the ids of the request that
	// published, carried so a task's log lines name it. It is written before
	// the enqueue stamp because that receiver is connected first, and its
	// fields keep the order they are given rather than being sorted: Python
	// writes a dict in insertion order.
	LogContext []LogField

	// EnqueuedAtNs is this app's own enqueue stamp
	// (before_task_publish in app/celery_app.py): CLOCK_MONOTONIC
	// nanoseconds, not a wall clock, and the only source of true queue wait.
	EnqueuedAtNs string
}

// LogField is one field of the log-context header.
type LogField struct {
	Key   string
	Value string
}

// Encode writes the envelope exactly as kombu's Redis transport writes one.
func Encode(t Task) ([]byte, error) {
	if err := t.validate(); err != nil {
		return nil, err
	}

	body, err := t.body()
	if err != nil {
		return nil, err
	}
	headers, err := t.headers()
	if err != nil {
		return nil, err
	}

	envelope := pyObject{}.
		add("body", pyString(base64.StdEncoding.EncodeToString([]byte(body)))).
		add("content-encoding", pyString("utf-8")).
		add("content-type", pyString("application/json")).
		add("headers", headers.encode()).
		add("properties", t.properties().encode())
	return []byte(envelope.encode()), nil
}

func (t Task) validate() error {
	switch {
	case t.Name == "":
		return errors.New("celery: a published task needs a name")
	case t.ID == "":
		return errors.New("celery: a published task needs an id")
	case t.RoutingKey == "":
		return errors.New("celery: a published task needs a routing key")
	case t.DeliveryTag == "":
		return errors.New("celery: a published task needs a delivery tag")
	}
	return nil
}

// body is protocol 2's [args, kwargs, embed]. The embed part is written as
// Celery writes it for a task with no callbacks, errbacks, chain or chord,
// which is every task this scheduler publishes.
func (t Task) body() (string, error) {
	args, err := pyJSON(anyArgs(t.Args))
	if err != nil {
		return "", fmt.Errorf("celery: args: %w", err)
	}
	kwargs, err := pyJSON(anyKwargs(t.Kwargs))
	if err != nil {
		return "", fmt.Errorf("celery: kwargs: %w", err)
	}
	embed := pyObject{}.
		add("callbacks", "null").
		add("errbacks", "null").
		add("chain", "null").
		add("chord", "null")
	return "[" + args + ", " + kwargs + ", " + embed.encode() + "]", nil
}

// headers is Celery's protocol 2 header set in Celery's own order, down to the
// fields it always writes null, because the comparison that proves this right
// is a comparison of bytes.
func (t Task) headers() (pyObject, error) {
	argsrepr, err := pyRepr(t.argsForRepr())
	if err != nil {
		return nil, fmt.Errorf("celery: argsrepr: %w", err)
	}
	kwargsrepr, err := pyRepr(anyKwargs(t.Kwargs))
	if err != nil {
		return nil, fmt.Errorf("celery: kwargsrepr: %w", err)
	}

	headers := pyObject{}.
		add("lang", pyString("py")).
		add("task", pyString(t.Name)).
		add("id", pyString(t.ID)).
		add("shadow", "null").
		add("eta", isoformat(t.ETA)).
		add("expires", isoformat(t.Expires)).
		add("group", "null").
		add("group_index", "null").
		add("retries", strconv.Itoa(t.Retries)).
		add("timelimit", t.timelimit()).
		add("root_id", pyString(t.RootID)).
		add("parent_id", optionalString(t.ParentID)).
		add("argsrepr", pyString(argsrepr)).
		add("kwargsrepr", pyString(kwargsrepr)).
		add("origin", pyString(t.Origin)).
		add("ignore_result", pyBool(t.IgnoreResult)).
		add("replaced_task_nesting", "0").
		add("stamped_headers", "null").
		add("stamps", "{}")
	// Both of this app's own headers are added by before_task_publish
	// receivers, after Celery has built the header set, in the order those
	// receivers were connected.
	if len(t.LogContext) > 0 {
		fields := pyObject{}
		for _, field := range t.LogContext {
			fields = fields.add(field.Key, pyString(field.Value))
		}
		headers = headers.add(logContextHeader, fields.encode())
	}
	if t.EnqueuedAtNs != "" {
		headers = headers.add(enqueuedAtHeader, pyString(t.EnqueuedAtNs))
	}
	return headers, nil
}

func (t Task) properties() pyObject {
	deliveryInfo := pyObject{}.
		add("exchange", pyString(t.Exchange)).
		add("routing_key", pyString(t.RoutingKey))
	properties := pyObject{}.
		add("correlation_id", pyString(t.ID)).
		add("reply_to", pyString(t.ReplyTo)).
		add("delivery_mode", "2")
	if t.Expiration > 0 {
		properties = properties.add("expiration",
			pyString(strconv.FormatInt(t.Expiration.Milliseconds(), 10)))
	}
	return properties.
		add("delivery_info", deliveryInfo.encode()).
		add("priority", strconv.Itoa(t.Priority)).
		add("body_encoding", pyString("base64")).
		add("delivery_tag", pyString(t.DeliveryTag))
}

// timelimit is Celery's own [time_limit, soft_time_limit] — the hard limit
// first. The order reads backwards and it is the order celery/app/amqp.py
// writes and celery/worker/request.py unpacks, so a message that wrote the
// soft limit first would be hard-killed at the soft limit by a Python worker.
func (t Task) timelimit() string {
	hard, soft := "null", "null"
	if t.HardTimeLimit > 0 {
		hard = strconv.Itoa(int(t.HardTimeLimit / time.Second))
	}
	if t.SoftTimeLimit > 0 {
		soft = strconv.Itoa(int(t.SoftTimeLimit / time.Second))
	}
	return "[" + hard + ", " + soft + "]"
}

// argsForRepr keeps Celery's own distinction. `args or ()` in apply_async
// makes a task published with no positional arguments carry a tuple whatever
// the call site passed, whose repr is "()"; past that, a tuple is a tuple and a
// list is a list.
func (t Task) argsForRepr() any {
	if len(t.Args) == 0 {
		return pyTuple{}
	}
	if t.ArgsAreTuple {
		return pyTuple(t.Args)
	}
	return anyArgs(t.Args)
}

// pyTuple is a Python tuple, which reprs differently from a list and encodes
// as JSON identically.
type pyTuple []any

// logContextHeader is app/observability/logs/celery_signals.py's _HEADER.
const logContextHeader = "soloclef_log_context"

// enqueuedAtHeader is the header the stamp lives in. It keeps the surfsense
// spelling because the producer that writes it is Python's, unchanged
// (custom/docs/go-worker.md).
const enqueuedAtHeader = "surfsense.enqueued_at_ns"

// isoformat writes a time the way Python's datetime.isoformat does for an
// aware UTC datetime, which is the format Celery's own eta and expires
// headers carry.
//
// The microseconds are all six digits or none at all. Go's own ".999999" drops
// trailing zeros and Python's isoformat does not, so a time whose microsecond
// field ends in a zero — about one eta in ten, and every eta comes from a wall
// clock — would be written ".12345" where Celery writes ".123450". Both parse;
// only one is the envelope Celery's client wrote.
func isoformat(at time.Time) string {
	if at.IsZero() {
		return "null"
	}
	// Python's datetime holds microseconds, so a finer reading is not a time
	// Celery could have published.
	at = at.UTC().Truncate(time.Microsecond)
	written := at.Format("2006-01-02T15:04:05")
	if micro := at.Nanosecond() / 1000; micro != 0 {
		written += fmt.Sprintf(".%06d", micro)
	}
	return pyString(written + at.Format("-07:00"))
}

func optionalString(value string) string {
	if value == "" {
		return "null"
	}
	return pyString(value)
}

func pyBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func anyArgs(args []any) []any {
	if args == nil {
		return []any{}
	}
	return args
}

func anyKwargs(kwargs map[string]any) map[string]any {
	if kwargs == nil {
		return map[string]any{}
	}
	return kwargs
}
