// Package pushsend is what a push sender is, whichever service it sends
// through: one message to one phone, the service's answer for it, and the
// interface the delivery job (soloclef_notify/delivery) sends with.
//
// Two senders implement it: soloclef_notify/expo, Expo's push service, which
// relays to Google's FCM for Android phones, and soloclef_notify/apns,
// Apple's APNs, which iPhones are pushed through directly. The delivery job
// picks one for each phone by the kind its token was registered with
// (push_devices.kind), so neither sender ever sees the other's tokens.
//
// The answer is normalised here, so the job decides from one vocabulary
// whatever the service: accepted, gone for good (forget the token), worth
// trying again later, or refused for another reason (log it).
package pushsend

import (
	"context"
	"errors"
	"time"
)

// The kinds of token a phone registers (push_devices.kind), as the API
// stores them and the delivery job routes them: one spelling for both.
const (
	// KindExpo is an Expo push token, "ExponentPushToken[...]": Android
	// phones, pushed through Expo's service.
	KindExpo = "expo"
	// KindAPNs is an APNs device token in hex: iPhones, pushed through
	// Apple directly.
	KindAPNs = "apns"
)

// MaxBatch is the most messages one Send takes. It is Expo's limit for one
// request; APNs takes one message a request, and its sender spreads a batch
// over parallel streams of one connection, so the same bound keeps one
// call's work and memory small for both.
const MaxBatch = 100

// MaxReceiptBatch is the most ticket ids one Receipts call takes (Expo's
// limit).
const MaxReceiptBatch = 1000

// Message is one push to one phone. A sender sends the fields its service
// knows and ignores the others (Expo has no thread or collapse id; APNs has
// no Android channel).
type Message struct {
	// To is the phone's token in its own service's form.
	To    string
	Title string
	Body  string
	// Data is what the app reads when the push is tapped: kind, path, v and
	// server (delivery builds it). It never carries a note's text or mail.
	Data map[string]any
	// Sound "default" plays the phone's own notification sound.
	Sound string
	// Badge is the number on the app's icon; nil leaves it as it is.
	Badge *int
	// ChannelID is the Android channel the app made at start ("soloclef").
	ChannelID string
	// ThreadID groups an iPhone's notifications on the lock screen (the
	// push's kind), so a burst of one kind stacks together.
	ThreadID string
	// CollapseID makes a newer push with the same id replace the older one
	// on an iPhone instead of adding to it: the collapse key of a row that
	// stands for several events. "" for a push that never collapses.
	CollapseID string
	// TTL is how long the service keeps trying a phone that is off; after
	// it the push is dropped (the inbox row stays). 0 is the service's own
	// default.
	TTL time.Duration
}

// Ticket is the service's answer for one message.
type Ticket struct {
	// Accepted is true when the service took the message.
	Accepted bool
	// ReceiptID is set when the service will say later what became of an
	// accepted message (Expo's ticket id); "" when it will not (APNs answers
	// at once and keeps nothing to ask about).
	ReceiptID string
	// Reference is the service's id for this push (APNs's apns-id), for the
	// log only: it is what Apple's tools look a push up by.
	Reference string
	// Gone is true when the token will never work again: the app was
	// removed, or its permission withdrawn. The token is deleted.
	Gone bool
	// Retry is true when this message failed for a reason that may pass (a
	// rate limit, a server error, a dropped connection) and may be sent again
	// later.
	Retry bool
	// Error is the service's code for a failure ("BadDeviceToken",
	// "MessageTooBig", ...), "" when accepted; Message its sentence. Both are
	// for the log and name no content.
	Error   string
	Message string
}

// Receipt is what became of an accepted message once the phone's own
// service answered.
type Receipt struct {
	// Gone is true when the token turned out never to work again.
	Gone bool
	// Error is "" when the push was delivered to Apple or Google.
	Error   string
	Message string
}

// Sender sends pushes.
type Sender interface {
	// Send sends at most MaxBatch messages and answers one ticket per
	// message, in the messages' order. An error wrapping ErrTransient means
	// the whole call may be tried again later; any other error means it will
	// fail again the same way. A sender whose service answers per message
	// (APNs) never fails the whole call for one message: it says so in that
	// message's ticket.
	Send(ctx context.Context, messages []Message) ([]Ticket, error)
}

// ReceiptReader reads what became of accepted messages, for a service that
// answers later (Expo).
type ReceiptReader interface {
	// Receipts answers the receipts the service has for at most
	// MaxReceiptBatch receipt ids. An id with no receipt yet is absent from
	// the map.
	Receipts(ctx context.Context, ids []string) (map[string]Receipt, error)
}

// ErrTransient is a call that failed for a reason that may pass: a rate
// limit, a server error, a dropped connection.
var ErrTransient = errors.New("pushsend: the push service did not take the request; it may be tried again")
