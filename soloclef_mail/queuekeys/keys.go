// Package queuekeys names the Redis keys the mail queue lives in.
//
// Both halves need them -- a producer to push onto ready, the sender process
// to take, hold, recover and retry -- and they are app/mail_queue/queue_store.py's
// keys, not new ones: a Go producer writes onto the same list the Python
// mail_sender is draining, and a Go sender takes off the same list a Python
// request pushed onto.
package queuekeys

import "strconv"

// Prefix is queue_store.py's PREFIX. The old product word is not in it; it
// never was.
const Prefix = "soloclef:mail"

// Ready is the list new mail is pushed onto, on the left, and taken from on
// the right: oldest first.
const Ready = Prefix + ":ready"

// Retry is the sorted set of failed mail, scored by the time it is due again.
const Retry = Prefix + ":retry"

// SendingPattern matches every sender's held list, including a stopped run's.
const SendingPattern = Prefix + ":sending:*"

// Sending names the list holding what sender n has in hand right now. A
// message is moved there in the same operation that takes it, so a sender that
// dies mid-send loses nothing.
func Sending(sender int) string {
	return Prefix + ":sending:" + strconv.Itoa(sender)
}
