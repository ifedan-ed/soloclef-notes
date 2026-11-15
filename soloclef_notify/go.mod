// Notifications, shared by every tier that finishes work for a person.
//
// When something a person started finishes or fails (a Studio output, an
// import, an automation run, a mail draft waiting to be confirmed), one
// inbox row is written and, where the person allows it, one push is sent to
// their phone. The API, the worker and the indexer all finish such work, so
// the step lives here, in one place: a second copy in each tier would be a
// second idea of what a notification says, how failures collapse, and who
// may be woken by one.
//
//   - notify: the one notification step (write the row, schedule the push)
//   - pushpolicy: the kinds of push, the person's switches and quiet hours
//   - presence: the "is this person looking at Soloclef now" signal's keys
//   - livechange: the live-change bus, "read this again" for a person's
//     open web tabs and app (the API streams it to them)
//   - pushsend: what a push sender is (a message, the service's answer)
//   - apns: the sender for iPhones, straight to Apple over HTTP/2
//   - expo: the sender for Android phones, through Expo's service
//   - delivery: the worker's jobs that send pushes and read receipts
//
// It requires pgx and the Celery protocol module and nothing else: each tier
// reaches Valkey with its own client, so the three calls on it (the presence
// read, a fence and a live-change publish) are interfaces a tier satisfies in a
// few lines.
module git.danvics.com/danvics/soloclef/soloclef_notify

go 1.24.4

require (
	git.danvics.com/danvics/soloclef/soloclef_celery v0.0.0
	github.com/jackc/pgx/v5 v5.7.1
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.27.0 // indirect
	golang.org/x/sync v0.8.0 // indirect
	golang.org/x/text v0.18.0 // indirect
)

// The Celery envelope every tier publishes with, from this checkout: one
// spelling of a message on both ends of the queue.
replace git.danvics.com/danvics/soloclef/soloclef_celery => ../soloclef_celery
