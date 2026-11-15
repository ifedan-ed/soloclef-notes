// Package pushpolicy decides whether a notification may be pushed to a
// person's phone: which kinds of push there are, the person's switch for
// each, and their quiet hours. It is pure (no database, no clock of its own),
// so the API that saves the choices and the worker that obeys them read one
// definition of each.
//
// The inbox row is never subject to any of this: it is always written. These
// rules only decide whether the phone is woken for it.
package pushpolicy

// The kinds of push. Each is a switch on Settings -> Notifications (web) and
// More -> Notifications (phone); both draw exactly the list the API sends,
// so a kind added here shows up on both without a client release.
const (
	Output     = "output"
	Source     = "source"
	Automation = "automation"
	Mail       = "mail"
	// Update is "Soloclef 1.2.0 is out", to an iPhone running an older
	// version than the App Store offers (soloclef_worker/internal/appupdate).
	Update = "update"
)

// Kind is one switch as the clients draw it.
type Kind struct {
	Kind        string `json:"kind"`
	Label       string `json:"label"`
	Description string `json:"description"`
}

// Kinds is every kind, in the order the screens list them. Only events
// something actually sends are offered: a switch for a push that never comes
// would tell the person a lie about what they will be woken for. (The chat's
// "answer" and "approval" pushes are not sent by anything, so they are not
// offered.)
var Kinds = []Kind{
	{Output, "Studio outputs", "An audio overview, video, report, slides or other output is ready, or could not be made."},
	{Source, "Sources", "An import could not be added, or a large or slow one is ready."},
	{Automation, "Automations", "A run finished, failed or needs your attention."},
	{Mail, "Mail drafts", "A mail draft is waiting for you to confirm it."},
	// iPhone only: the Android app has no Play Store listing to compare
	// with, so nothing tells an Android phone about an update.
	{Update, "App updates", "A new version of the iPhone app is in the App Store."},
}

// Known reports whether name is a kind.
func Known(name string) bool {
	for _, kind := range Kinds {
		if kind.Kind == name {
			return true
		}
	}
	return false
}
