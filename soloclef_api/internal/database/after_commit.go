package database

// Effects collects what a write owes the rest of the system once its rows are
// durable, and runs it only then.
//
// A Python write fires its PostHog capture, its push event and its event-bus
// publish *after* session.commit() returns, and a route that raises before the
// commit fires none of them. So a Go write cannot call an effect where it
// decides on one: a transaction that rolls back afterwards would have told
// PostHog about a role nobody created. Registering the effect instead keeps the
// decision where the data is and the call where the commit is.
//
// An effect must not fail the request, because none of the Python ones can: the
// analytics client swallows its own errors and the push enqueue logs and
// returns. So an effect takes nothing and returns nothing, and anything it
// cannot do it reports to its own logger.
type Effects struct {
	pending []func()
}

// After registers an effect to run once the transaction has committed.
func (e *Effects) After(effect func()) {
	e.pending = append(e.pending, effect)
}

// run fires the effects in the order they were registered, which is the order
// the Python route's own calls are written in.
func (e *Effects) run() {
	for _, effect := range e.pending {
		effect()
	}
}
