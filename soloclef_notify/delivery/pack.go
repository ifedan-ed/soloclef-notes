package delivery

import "git.danvics.com/danvics/soloclef/soloclef_notify/pushsend"

// route is the sender a phone is pushed by, from its token's kind.
type route int

const (
	viaExpo route = iota
	viaAPNs
)

func (r route) String() string {
	if r == viaAPNs {
		return "apns"
	}
	return "expo"
}

// outgoing is one row's push to those of its owner's phones that one sender
// pushes. devices[i] is the push_devices id messages[i] goes to, so a
// message that must be tried again names its phone.
type outgoing struct {
	notificationID int64
	route          route
	devices        []int64
	messages       []pushsend.Message
}

// batch is one Send: whole rows of one sender, at most pushsend.MaxBatch
// messages. rows[i] and devices[i] are messages[i]'s row and phone.
type batch struct {
	route    route
	rows     []int64
	devices  []int64
	messages []pushsend.Message
}

// pack puts rows into calls of at most pushsend.MaxBatch messages, one
// sender's rows per call and in the order given, never splitting a row's
// phones of one sender across two calls. A row with more phones than a call
// holds (nobody has a hundred) is sent alone, cut to the limit.
func pack(rows []outgoing) []batch {
	var batches []batch
	open := map[route]*batch{}
	var order []route
	for _, row := range rows {
		messages, devices := row.messages, row.devices
		if len(messages) > pushsend.MaxBatch {
			messages, devices = messages[:pushsend.MaxBatch], devices[:pushsend.MaxBatch]
		}
		current := open[row.route]
		if current == nil {
			current = &batch{route: row.route}
			open[row.route] = current
			order = append(order, row.route)
		}
		if len(current.messages)+len(messages) > pushsend.MaxBatch {
			batches = append(batches, *current)
			*current = batch{route: row.route}
		}
		for range messages {
			current.rows = append(current.rows, row.notificationID)
		}
		current.devices = append(current.devices, devices...)
		current.messages = append(current.messages, messages...)
	}
	for _, r := range order {
		if current := open[r]; len(current.messages) > 0 {
			batches = append(batches, *current)
		}
	}
	return batches
}
