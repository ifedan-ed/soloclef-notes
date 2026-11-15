// Package presence spells the signal that says a person is looking at
// Soloclef right now: a visible web tab, or the phone app in the foreground.
// While it is on, nothing is pushed to their phone; they will see the
// notification where they are (the web's inbox updates live, the app shows a
// toast).
//
// # Why a heartbeat, and why this one
//
// The server cannot otherwise tell a visible tab from a forgotten one, or a
// phone in the foreground from one in a pocket. The lightest signal that can
// is what this package spells:
//
//   - Each client sends `PUT /api/v1/presence {client_id, active}` when it
//     becomes visible (or comes to the foreground), every Beat while it
//     stays so, and `active: false` the moment it is hidden. A hidden tab or
//     a backgrounded app sends nothing at all.
//   - The API writes one member of one sorted set in Valkey and touches no
//     Postgres row: the member is the client, its score the instant its mark
//     runs out. The whole set expires a TTL after the last beat, so a person
//     who closed everything leaves nothing behind.
//   - The worker asks one ZCOUNT before a push. Several tabs and a phone are
//     several members, so one tab being hidden never hides another.
//
// One small request a minute per visible client costs less than the polling
// the clients already do, and nothing at all while nobody is looking. A
// client that vanishes without saying so (a crash, a lost network) stops
// counting within TTL, so at worst a push is held back for two and a half
// minutes, never for ever.
package presence

import (
	"regexp"
	"strconv"
	"time"
)

// Beat is how often a visible client says so. TTL is how long one beat
// counts: two and a half beats, so one late or lost beat (a slow network, a
// timer throttled by the browser) does not drop a person who is there.
const (
	Beat = 60 * time.Second
	TTL  = 150 * time.Second
)

// keyPrefix is the sorted set's key, one per account.
const keyPrefix = "soloclef:presence:"

// Key is the account's sorted set: members are client ids, scores the
// Unix millisecond their mark runs out.
func Key(userID string) string { return keyPrefix + userID }

// Expiry is the score a beat at now writes.
func Expiry(now time.Time) float64 { return float64(now.Add(TTL).UnixMilli()) }

// LiveFrom is the lowest score that still counts at now, as ZCOUNT's min:
// a mark that runs out exactly now has run out.
func LiveFrom(now time.Time) string {
	return "(" + strconv.FormatInt(now.UnixMilli(), 10)
}

// clientID is what a client may name itself: random, short, and unable to
// carry anything else into Valkey.
var clientID = regexp.MustCompile(`^[A-Za-z0-9_-]{8,64}$`)

// ValidClientID reports whether the id is one a client may send.
func ValidClientID(id string) bool { return clientID.MatchString(id) }
