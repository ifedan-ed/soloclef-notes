package executor

import (
	"git.danvics.com/danvics/soloclef/soloclef_celery/celery"
)

// Call is one delivery as the task process reads it on stdin: the arguments
// the body takes, and the request fields Celery's own task context exposes to
// a bound task (self.request.id, .retries, .delivery_info).
//
// The task name is deliberately absent: it is the process's argument, so there
// is one source for it and no way for the two to disagree.
type Call struct {
	ID       string         `json:"id"`
	Args     []any          `json:"args"`
	Kwargs   map[string]any `json:"kwargs"`
	Retries  int            `json:"retries"`
	Queue    string         `json:"queue"`
	Exchange string         `json:"exchange"`
	RootID   string         `json:"root_id"`
	ParentID string         `json:"parent_id"`

	// Redelivered tells a body it may be running for the second time, which
	// is all acks_late can promise.
	Redelivered bool `json:"redelivered"`

	// Worker is what self.request.hostname reads back as.
	Worker string `json:"worker"`

	// Seconds, as Celery's timelimit header counts them, so a body that wants
	// to budget its own work sees the same numbers this worker enforces.
	SoftTimeLimit float64 `json:"soft_time_limit"`
	HardTimeLimit float64 `json:"hard_time_limit"`
}

// callFor is the delivery the process is given.
func callFor(msg *celery.Message, worker string, lim limits) Call {
	call := Call{
		ID:            msg.ID,
		Args:          msg.Args,
		Kwargs:        msg.Kwargs,
		Retries:       msg.Retries,
		Queue:         msg.Queue,
		Exchange:      msg.Exchange,
		RootID:        msg.RootID,
		ParentID:      msg.ParentID,
		Redelivered:   msg.Redelivered,
		Worker:        worker,
		SoftTimeLimit: lim.soft.Seconds(),
		HardTimeLimit: lim.hard.Seconds(),
	}
	if call.Args == nil {
		call.Args = []any{}
	}
	if call.Kwargs == nil {
		call.Kwargs = map[string]any{}
	}
	return call
}
