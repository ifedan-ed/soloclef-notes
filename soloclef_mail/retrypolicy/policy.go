// Package retrypolicy is when a temporarily failed email is tried again, and
// when we stop.
//
// app/mail_queue/retry_policy.py's numbers, unchanged: seven tries over about
// 45 minutes, long enough to ride out a provider blip or a rate limit and
// short enough that a code email is still worth having. They are not rewritten
// for the HTTP API transport -- a 429 and an SMTP 421 are the same answer
// ("later"), and the two senders must give up at the same try or a message
// drained by one is retried a different number of times than by the other.
package retrypolicy

import "time"

// Delays is RETRY_DELAYS_SECONDS: the wait before try 2, 3, ... after a
// temporary failure.
var Delays = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	60 * time.Second,
	180 * time.Second,
	600 * time.Second,
	1800 * time.Second,
}

// MaxAttempts is how many tries a message gets in all.
var MaxAttempts = len(Delays) + 1

// Delay is the wait before the next try after attemptsMade failures; ok is
// false when there are no tries left.
func Delay(attemptsMade int) (time.Duration, bool) {
	if attemptsMade < 1 || attemptsMade >= MaxAttempts {
		return 0, false
	}
	return Delays[attemptsMade-1], true
}
