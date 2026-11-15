package pushpolicy

import (
	"encoding/json"
	"time"
)

// Preferences is one person's push choices as stored: a switch per kind and
// their quiet hours, if any.
type Preferences struct {
	// Choices has exactly one entry per kind (Choices below).
	Choices map[string]bool
	// Quiet is nil when the person has no quiet hours.
	Quiet *QuietHours
}

// Choices is the saved switches over the defaults (every kind on until the
// person turns it off). A saved key that is no longer a kind, or a value that
// is not a switch, is left out, so the answer always has exactly one switch
// per kind and an old row can never switch on a kind by accident.
func Choices(saved []byte) map[string]bool {
	choices := make(map[string]bool, len(Kinds))
	for _, kind := range Kinds {
		choices[kind.Kind] = true
	}
	var stored map[string]any
	if len(saved) > 0 && json.Unmarshal(saved, &stored) == nil {
		for key, value := range stored {
			if on, isSwitch := value.(bool); isSwitch {
				if _, known := choices[key]; known {
					choices[key] = on
				}
			}
		}
	}
	return choices
}

// Verdict is what the policy decided about one push.
type Verdict uint8

const (
	// Send: the push may go.
	Send Verdict = iota + 1
	// SwitchedOff: the person turned this kind off.
	SwitchedOff
	// QuietNow: it is inside the person's quiet hours.
	QuietNow
	// UnknownKind: the push names no kind; it is not sent, because no
	// switch could ever have allowed it.
	UnknownKind
)

func (v Verdict) String() string {
	switch v {
	case Send:
		return "send"
	case SwitchedOff:
		return "switched off"
	case QuietNow:
		return "quiet hours"
	case UnknownKind:
		return "unknown kind"
	}
	return "unset"
}

// Decide says whether a push of this kind may be sent to the person now.
// Presence (whether they are looking at Soloclef) is not decided here: it
// is a read of Valkey the worker makes after this answers Send, so the
// cheap, pure checks go first.
func Decide(preferences Preferences, kind string, now time.Time) Verdict {
	on, known := preferences.Choices[kind]
	switch {
	case !known:
		return UnknownKind
	case !on:
		return SwitchedOff
	case preferences.Quiet != nil && preferences.Quiet.Covers(now):
		return QuietNow
	}
	return Send
}
