package notify

import (
	"slices"
	"strconv"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_celery/produce"
)

// The worker's tasks this step publishes (soloclef_notify/delivery runs
// them). They go on the default queue: a push is seconds of work and must
// never wait behind a podcast on the heavy one.
const (
	// DeliverTask pushes a batch of inbox rows: args [[id, ...]], and on a
	// retry kwargs {"devices": {"<id>": [device id, ...]}}.
	DeliverTask = "notifications.deliver_push"
	// ReceiptsTask reads Expo's receipts for pushes sent a while ago and
	// forgets the phones that are gone: no args.
	ReceiptsTask = "notifications.check_push_receipts"
)

// DeliverMessage is the message that pushes the rows after the wait.
// attempt is how many times delivery has already been tried; it travels as
// Celery's own retries count.
func DeliverMessage(ids []int64, attempt int, after time.Duration) produce.Message {
	list := make([]any, len(ids))
	for i, id := range ids {
		list[i] = id
	}
	return produce.Message{Task: DeliverTask, Args: []any{list}, Retries: attempt, Countdown: after}
}

// DevicesKwarg is the keyword of the phones a delivery is for: {"<row id>":
// [device id, ...]}. A first delivery of most rows has none and goes to every
// live phone of each row's owner; a retry names exactly the phones each row
// did not reach, so a phone that took the push the first time is never
// pushed it twice; and a row meant for some of the owner's phones only (an
// App Store update, for the iPhones on an older version) names them from
// its first delivery (DeliverToMessage).
const DevicesKwarg = "devices"

// DeliverToMessage is the first delivery of a row meant for only some of
// its owner's phones: the phones are named from the start, through the
// retry's own keyword, so the delivery job has one way to read "these
// phones only" and a retry of it keeps the same list.
func DeliverToMessage(row int64, devices []int64) produce.Message {
	return RetryMessage(map[int64][]int64{row: devices}, 0, 0)
}

// RetryMessage is the message that tries the rows again after the wait, for
// the phones named for each. attempt is how many times they were tried.
func RetryMessage(devices map[int64][]int64, attempt int, after time.Duration) produce.Message {
	ids := make([]int64, 0, len(devices))
	named := make(map[string]any, len(devices))
	for row, phones := range devices {
		ids = append(ids, row)
		list := make([]any, len(phones))
		for i, phone := range phones {
			list[i] = phone
		}
		named[strconv.FormatInt(row, 10)] = list
	}
	// In order, so the same retry is the same message (and reads the same
	// in a log).
	slices.Sort(ids)
	message := DeliverMessage(ids, attempt, after)
	message.Kwargs = map[string]any{DevicesKwarg: named}
	return message
}

// ReceiptsMessage is the message that reads receipts after the wait.
func ReceiptsMessage(after time.Duration) produce.Message {
	return produce.Message{Task: ReceiptsTask, Args: []any{}, Countdown: after}
}
