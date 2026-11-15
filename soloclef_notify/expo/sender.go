// Package expo sends pushes to phones through Expo's push service, which
// relays them to Google's FCM with the credentials Expo holds for the
// Soloclef app. Android phones are pushed this way; iPhones are pushed
// straight through Apple (soloclef_notify/apns), and a row registered before
// that (push_devices.kind 'expo' on an iPhone) through Expo to APNs until
// the phone registers again.
//
// Client is pushsend.Sender and pushsend.ReceiptReader, so the delivery job
// sends through it exactly as through the APNs sender.
//
// Expo relays what it is given, so a message carries a generic title, a
// notebook's name at most, and a path: never a note's text, mail, or a
// passage of a document (soloclef_notify/delivery builds them).
package expo

import "time"

// Message is one push in the fields of Expo's push API that this app uses:
// the JSON Client sends, and what expotest decodes.
type Message struct {
	To    string         `json:"to"`
	Title string         `json:"title,omitempty"`
	Body  string         `json:"body,omitempty"`
	Data  map[string]any `json:"data,omitempty"`
	// Sound "default" plays the phone's own notification sound.
	Sound string `json:"sound,omitempty"`
	// Badge is the number on the app's icon; nil leaves it as it is.
	Badge *int `json:"badge,omitempty"`
	// ChannelID is the Android channel the app made at start ("soloclef").
	ChannelID string `json:"channelId,omitempty"`
	// TTL is how many seconds APNs/FCM keep trying a phone that is off;
	// 0 is the services' own default.
	TTL int `json:"ttl,omitempty"`
}

// The error codes of Expo's that the client turns into a decision.
const (
	// DeviceNotRegistered is a token whose app was removed or whose push
	// permission was withdrawn: it will never work again (Ticket.Gone).
	DeviceNotRegistered = "DeviceNotRegistered"
	// MessageRateExceeded is too many pushes to one phone too quickly: the
	// message may go later (Ticket.Retry).
	MessageRateExceeded = "MessageRateExceeded"
)

// ttlSeconds is a TTL in Expo's unit, whole seconds, rounded up so a short
// TTL is never sent as 0 (which would mean the service's default).
func ttlSeconds(ttl time.Duration) int {
	if ttl <= 0 {
		return 0
	}
	return int((ttl + time.Second - 1) / time.Second)
}
