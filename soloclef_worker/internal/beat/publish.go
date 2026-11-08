package beat

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// publisher puts a tick on a queue through the producer both Go tiers publish
// with (soloclef_celery/produce), so a scheduled task and a task enqueued by
// the API tier are the same bytes on the same list.
//
// It declares nothing. A queue's exchange and its binding are declared by the
// consumers that read it, as they are today, and a message routed through the
// anonymous exchange is put on its list without the binding table being read
// at all (custom/docs/go-api-tier.md, "What a Go producer has to declare").
type publisher struct {
	producer produce.Producer
}

func newPublisher(rdb redis.UniversalClient, origin, replyTo string) publisher {
	return publisher{producer: produce.Producer{
		Broker:  redisBroker{rdb: rdb},
		Origin:  origin,
		ReplyTo: replyTo,
	}}
}

// publish writes one tick and returns the task id it was given.
func (p publisher) publish(ctx context.Context, entry Entry, now time.Time) (string, error) {
	// The schedule's clock, not the producer's: an expiry computed from
	// anything else would not be the window this tick was due in.
	producer := p.producer
	producer.Now = func() time.Time { return now }
	return producer.Send(ctx, produce.Message{
		Task: entry.Task,
		// Each entry in app/celery_app.py's beat_schedule names its queue and
		// carries no arguments.
		Queue: entry.Queue,
		// Computed at publish, as Celery computes it: a tick nobody ran inside
		// its window is dropped rather than run late.
		ExpiresIn: entry.Expires,
	})
}
