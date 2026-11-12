package podcasts

import (
	"errors"
	"strconv"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/voices"
)

// A new podcast arrives at the brief gate with a brief already written, so
// that the common case is one press of "approve". This file writes that
// brief. It costs nothing -- no model is asked -- and every choice in it is
// one the user can change at the gate.
//
// The rules, each of which has exactly one outcome:
//
//   - Language: the language of the caller's last brief, if the roster
//     still has a voice for it; otherwise English. The source text is never
//     examined to guess one -- a language the user did not ask for is worse
//     than a predictable default.
//   - Voices, speaker by speaker: the voice that speaker had in the last
//     brief; failing that the first of the deployment's configured hosts
//     nobody has yet; failing that a voice nobody has yet, preferring a
//     gender nobody has, so that two speakers sound like two people. A
//     voice the roster no longer has, or that does not speak the language,
//     is passed over. (The Python proposal made one list of last time's
//     voices followed by the hosts and read it by position, so a first
//     speaker whose old voice was gone got an arbitrary voice while the
//     configured host went to the third.)
//   - Everything else is fixed: a conversation, speakers named for their
//     roles, the length and the focus the request gave.

// defaultLanguage is what an account with no usable last brief gets.
const defaultLanguage = "en"

// errNoDefaultVoice is a roster with no voice for the default language. No
// roster in soloclef_assistant/voices is like that; if one ever is, a podcast cannot
// be proposed and the answer is the catalogue's "not set up correctly".
var errNoDefaultVoice = &voices.MisconfiguredError{
	Setting: voices.ServiceVariable,
	Reason:  "names a service with no voice for the default language, " + defaultLanguage,
}

// The role each speaker is given, by slot. A sixth speaker is a guest.
var roleBySlot = []string{"host", "guest", "expert", "cohost", "narrator"}

// lastChoices is what the caller chose last time.
type lastChoices struct {
	language string
	// voices is the voice ids of the last brief's speakers, in their order.
	voices []string
}

// choicesFrom reads the last brief's choices out of its column. A brief that
// cannot be read has none: a new podcast starts from the defaults rather
// than failing because an old one is damaged.
func choicesFrom(column any) (lastChoices, bool) {
	if column == nil {
		return lastChoices{}, true
	}
	last, wrong := parseBrief(column, stored, nil)
	if len(wrong) > 0 {
		return lastChoices{}, false
	}
	choices := lastChoices{language: last.language}
	for _, speaker := range last.speakers {
		choices.voices = append(choices.voices, speaker.voice)
	}
	return choices, true
}

// propose writes the brief of a new podcast.
func propose(roster *voices.Roster, last lastChoices, request creation) (*pythonjson.Object, error) {
	language := defaultLanguage
	if last.language != "" && roster.Supports(last.language) {
		language = last.language
	}
	available := roster.ForLanguage(language)
	if len(available) == 0 {
		return nil, errNoDefaultVoice
	}

	cast := castVoices(available, last.voices, roster.Hosts(language), int(request.SpeakerCount))
	if len(cast) != int(request.SpeakerCount) {
		return nil, errors.New("a speaker was left without a voice")
	}
	speakers := make([]any, 0, len(cast))
	for slot, voice := range cast {
		speaker := pythonjson.NewObject()
		speaker.Set("slot", int64(slot))
		speaker.Set("name", nameForSlot(slot))
		speaker.Set("role", roleForSlot(slot))
		speaker.Set("voice_id", voice.ID)
		// A delivery note is written by voice casting, later, if at all.
		speaker.Set("delivery", nil)
		speakers = append(speakers, speaker)
	}

	duration := pythonjson.NewObject()
	duration.Set("min_seconds", request.MinSeconds)
	duration.Set("max_seconds", request.MaxSeconds)

	brief := pythonjson.NewObject()
	brief.Set("language", language)
	brief.Set("style", defaultStyle)
	brief.Set("speakers", speakers)
	brief.Set("duration", duration)
	if request.Focus == nil {
		brief.Set("focus", nil)
	} else {
		brief.Set("focus", *request.Focus)
	}
	return brief, nil
}

// castVoices gives each of `count` speakers a voice out of `available`, the
// voices that speak the brief's language. For each speaker in turn, the
// first of these that yields a voice nobody has yet:
//
//  1. the voice that speaker had last time;
//  2. the first configured host;
//  3. a voice of a gender nobody has, then any voice.
//
// When there are more speakers than voices -- Kokoro has one French voice --
// the first voice is used again: every speaker gets a voice, and the user
// sees the repeat at the gate and can change the language or the cast.
func castVoices(available []voices.Voice, lastUsed []string, hosts []voices.Voice, count int) []voices.Voice {
	byID := make(map[string]voices.Voice, len(available))
	for _, voice := range available {
		byID[voice.ID] = voice
	}
	cast := make([]voices.Voice, 0, count)
	taken := map[string]bool{}
	heard := map[voices.Gender]bool{}
	for slot := 0; slot < count; slot++ {
		var chosen voices.Voice
		if slot < len(lastUsed) && !taken[lastUsed[slot]] {
			// The zero Voice if last time's is not among the available.
			chosen = byID[lastUsed[slot]]
		}
		if chosen.ID == "" {
			for _, host := range hosts {
				if !taken[host.ID] {
					chosen = host
					break
				}
			}
		}
		if chosen.ID == "" {
			chosen = unusedVoice(available, taken, heard)
		}
		cast = append(cast, chosen)
		taken[chosen.ID] = true
		heard[chosen.Gender] = true
	}
	return cast
}

// unusedVoice is the first voice nobody has whose gender nobody has; failing
// that the first voice nobody has; failing that the first voice.
func unusedVoice(available []voices.Voice, taken map[string]bool, heard map[voices.Gender]bool) voices.Voice {
	var spare *voices.Voice
	for index, voice := range available {
		if taken[voice.ID] {
			continue
		}
		if !heard[voice.Gender] {
			return voice
		}
		if spare == nil {
			spare = &available[index]
		}
	}
	if spare != nil {
		return *spare
	}
	return available[0]
}

func roleForSlot(slot int) string {
	if slot < len(roleBySlot) {
		return roleBySlot[slot]
	}
	return "guest"
}

// nameForSlot is what a speaker is called until the user names them: their
// role, capitalised ("Host", "Co-Host"), and for a sixth speaker "Guest 5".
func nameForSlot(slot int) string {
	label := map[string]string{
		"host": "Host", "guest": "Guest", "expert": "Expert",
		"cohost": "Co-Host", "narrator": "Narrator",
	}[roleForSlot(slot)]
	if slot < len(roleBySlot) {
		return label
	}
	return label + " " + strconv.Itoa(slot)
}
