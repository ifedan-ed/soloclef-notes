package podcasts

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// This file reads a stored brief (`podcast_runs.spec`, a PodcastSpec as
// app/podcasts/schemas/spec.py defines it) and writes it out in the shape the
// web app's `podcastSpec` schema parses.
//
// The brief was validated when it was written. It is checked again on the
// way out because the column is jsonb and outlives the code that wrote it: a
// brief missing a field the web app requires would make the client's own
// parse fail on the whole podcast. So a brief is either rendered complete, or
// reported as unreadable and rendered as null -- never half of one.

// The members of PodcastStyle and SpeakerRole.
var (
	styles = []string{"conversational", "interview", "debate", "monologue", "narrative"}
	roles  = []string{"host", "cohost", "guest", "expert", "narrator"}
)

const (
	// The style of a brief that names none.
	defaultStyle = "conversational"
	// One voice is what "monologue" means.
	monologue = "monologue"
	// MAX_SPEAKERS.
	maxSpeakers = 6
	// The longest name, delivery note and focus a brief may carry.
	maxNameCharacters     = 120
	maxDeliveryCharacters = 300
	maxFocusCharacters    = 2000
	// MIN_DURATION_SECONDS and MAX_DURATION_SECONDS
	// (app/podcasts/duration_limits.py).
	minDurationSeconds = 15
	maxDurationSeconds = 24 * 60 * 60
)

// languageTag is a BCP-47 primary subtag and any number of further subtags:
// "en", "en-US", "zh-Hant-TW". Which languages can be voiced is the voice
// catalogue's question, not the brief's.
var languageTag = regexp.MustCompile(`^[A-Za-z]{2,3}(-[A-Za-z0-9]{2,8})*$`)

// readSpec is the brief a column holds: nil when it holds none, the rendered
// brief when it holds one, and false when it holds something that is not a
// complete brief.
func readSpec(column any) (*pythonjson.Object, bool) {
	if column == nil {
		return nil, true
	}
	read, wrong := parseBrief(column, stored, nil)
	if len(wrong) > 0 {
		return nil, false
	}
	return read.rendered, true
}

// A brief is read in two places, and by one function so that there is one
// definition of what a brief is: out of the column (stored), and out of a
// request that edits it (edited). They differ in one thing only.
const (
	// stored also reads a length written in minutes, which is how briefs
	// were sized before 2026-09. Old rows still hold them.
	stored = true
	// edited takes seconds only: nothing writes minutes any more, and a
	// request that does is a mistake to be told about.
	edited = false
)

// readBrief is a brief that has been read: how it is rendered and stored, and
// the parts of it a route goes on to ask about.
type readBrief struct {
	// rendered is the brief with every field present, in the contract's
	// order. It is both what the web app is answered and what is stored.
	rendered *pythonjson.Object
	language string
	speakers []briefSpeaker
}

type briefSpeaker struct {
	slot  int64
	voice string
	// rendered is this speaker inside brief.rendered, so a route can still
	// change it (brief_route.go keeps a delivery note an edit did not name).
	rendered *pythonjson.Object
	// deliveryNamed says the speaker carried a `delivery` key at all, null
	// included.
	deliveryNamed bool
}

// What a fault in a brief is called. Where a FastAPI route would have said
// the same thing, the words are its words, because clients already show them.
const (
	notAnObject     = "Input should be an object"
	notAList        = "Input should be a valid list"
	extraKey        = "Extra inputs are not permitted"
	notALanguage    = "Input should be a language tag such as en, en-US or pt-BR"
	blankText       = "Value error, must not be blank"
	notAWholeNumber = "Input should be a valid integer"
	// Postgres keeps U+0000 in neither text nor jsonb, so text holding one
	// is refused here by name instead of failing the statement with a 500.
	nulRefused       = "String should not contain a NUL character"
	negativeSlot     = "Input should be greater than or equal to 0"
	slotTaken        = "Value error, speaker slots must be unique"
	monologueOfMany  = "Value error, a monologue has exactly one speaker"
	durationReversed = "Value error, max_seconds must be >= min_seconds"
)

// faults collects what is wrong with a brief, each fault at the place it was
// found. Every fault is collected rather than the first, so that a form can
// mark all of its fields at once.
type faults struct {
	found []errorenvelope.Failure
}

func (f *faults) add(message string, base []string, path ...string) {
	location := append(append([]string{}, base...), path...)
	f.found = append(f.found, errorenvelope.Failure{Location: location, Message: message})
}

// parseBrief reads a brief out of a decoded JSON value. `at` is where the
// brief sits in whatever it was read from, and prefixes every fault.
//
// The brief is either read whole or not at all: with any fault the first
// result is not to be used.
func parseBrief(value any, fromColumn bool, at []string) (readBrief, []errorenvelope.Failure) {
	var wrong faults
	written, isObject := value.(*pythonjson.Object)
	if !isObject {
		wrong.add(notAnObject, at)
		return readBrief{}, wrong.found
	}
	// A key nobody declared means the brief was written by something this
	// code does not know, and guessing at half of it is worse than saying so.
	refuseOtherKeys(&wrong, written, at, "language", "style", "speakers", "duration", "focus")

	read := readBrief{rendered: pythonjson.NewObject()}

	language, present := written.Get("language")
	switch tag, accepted := readLanguage(language); {
	case !present:
		wrong.add(requestbody.FieldRequired, at, "language")
	case !accepted:
		if _, isText := language.(string); !isText {
			wrong.add(requestbody.NotAString, at, "language")
		} else {
			wrong.add(notALanguage, at, "language")
		}
	default:
		read.language = tag
	}

	style := defaultStyle
	if named, present := written.Get("style"); present {
		text, isText := named.(string)
		if !isText || !oneOf(text, styles) {
			wrong.add(requestbody.OneOf(styles), at, "style")
		} else {
			style = text
		}
	}

	rendered := []any{}
	switch speakers, isList := written.GetOrNil("speakers").([]any); {
	case !isList:
		if _, present := written.Get("speakers"); !present {
			wrong.add(requestbody.FieldRequired, at, "speakers")
		} else {
			wrong.add(notAList, at, "speakers")
		}
	case len(speakers) < 1:
		wrong.add("List should have at least 1 item after validation, not 0", at, "speakers")
	case len(speakers) > maxSpeakers:
		wrong.add("List should have at most "+strconv.Itoa(maxSpeakers)+" items after validation, not "+strconv.Itoa(len(speakers)), at, "speakers")
	default:
		// A transcript turn names its speaker by slot, so two speakers
		// cannot share one.
		taken := map[int64]bool{}
		for index, entry := range speakers {
			place := append(append([]string{}, at...), "speakers", strconv.Itoa(index))
			speaker, accepted := readSpeaker(&wrong, entry, place)
			if !accepted {
				continue
			}
			if taken[speaker.slot] {
				wrong.add(slotTaken, place, "slot")
				continue
			}
			taken[speaker.slot] = true
			read.speakers = append(read.speakers, speaker)
			rendered = append(rendered, speaker.rendered)
		}
		if style == monologue && len(speakers) != 1 {
			wrong.add(monologueOfMany, at, "speakers")
		}
	}

	duration := readDuration(&wrong, written, fromColumn, at)

	// A focus is optional, and one that is only spaces is none.
	var focus any
	if kept := written.GetOrNil("focus"); kept != nil {
		text, isText := kept.(string)
		switch {
		case !isText:
			wrong.add(requestbody.NotAString, at, "focus")
		case utf8.RuneCountInString(text) > maxFocusCharacters:
			wrong.add(atMostCharacters(maxFocusCharacters), at, "focus")
		case strings.ContainsRune(text, 0):
			wrong.add(nulRefused, at, "focus")
		default:
			if cleaned := strings.TrimSpace(text); cleaned != "" {
				focus = cleaned
			}
		}
	}

	read.rendered.Set("language", read.language)
	read.rendered.Set("style", style)
	read.rendered.Set("speakers", rendered)
	read.rendered.Set("duration", duration)
	read.rendered.Set("focus", focus)
	return read, wrong.found
}

func atMostCharacters(most int) string {
	return "String should have at most " + strconv.Itoa(most) + " characters"
}

// readLanguage is a language tag with its first part lowered: "PT-BR" is
// "pt-BR".
func readLanguage(value any) (string, bool) {
	written, isText := value.(string)
	if !isText {
		return "", false
	}
	cleaned := strings.TrimSpace(written)
	if !languageTag.MatchString(cleaned) {
		return "", false
	}
	primary, rest, hasRest := strings.Cut(cleaned, "-")
	if !hasRest {
		return strings.ToLower(primary), true
	}
	return strings.ToLower(primary) + "-" + rest, true
}

// readSpeaker is one voice of the brief.
func readSpeaker(wrong *faults, value any, at []string) (briefSpeaker, bool) {
	written, isObject := value.(*pythonjson.Object)
	if !isObject {
		wrong.add(notAnObject, at)
		return briefSpeaker{}, false
	}
	before := len(wrong.found)
	refuseOtherKeys(wrong, written, at, "slot", "name", "role", "voice_id", "delivery")

	speaker := briefSpeaker{rendered: pythonjson.NewObject()}
	slot, present := written.Get("slot")
	switch number, isNumber := wholeNumber(slot); {
	case !present:
		wrong.add(requestbody.FieldRequired, at, "slot")
	case !isNumber:
		wrong.add(notAWholeNumber, at, "slot")
	case number < 0:
		wrong.add(negativeSlot, at, "slot")
	default:
		speaker.slot = number
	}

	name := requiredText(wrong, written, "name", maxNameCharacters, at)

	role, present := written.Get("role")
	if text, isText := role.(string); !present {
		wrong.add(requestbody.FieldRequired, at, "role")
	} else if !isText || !oneOf(text, roles) {
		wrong.add(requestbody.OneOf(roles), at, "role")
	}

	// A voice id has no maximum: it is a catalogue key, not something typed.
	speaker.voice = requiredText(wrong, written, "voice_id", math.MaxInt, at)

	// How the speaker should sound, for a speech model that takes a style.
	// Kept as written.
	var delivery any
	kept, named := written.Get("delivery")
	speaker.deliveryNamed = named
	if kept != nil {
		text, isText := kept.(string)
		switch {
		case !isText:
			wrong.add(requestbody.NotAString, at, "delivery")
		case utf8.RuneCountInString(text) > maxDeliveryCharacters:
			wrong.add(atMostCharacters(maxDeliveryCharacters), at, "delivery")
		case strings.ContainsRune(text, 0):
			wrong.add(nulRefused, at, "delivery")
		default:
			delivery = text
		}
	}
	if len(wrong.found) > before {
		return briefSpeaker{}, false
	}

	speaker.rendered.Set("slot", speaker.slot)
	speaker.rendered.Set("name", name)
	speaker.rendered.Set("role", role)
	speaker.rendered.Set("voice_id", speaker.voice)
	speaker.rendered.Set("delivery", delivery)
	return speaker, true
}

// requiredText is a field that must be text, not blank and no longer than it
// may be, without the spaces around it.
func requiredText(wrong *faults, object *pythonjson.Object, name string, maxCharacters int, at []string) string {
	value, present := object.Get(name)
	text, isText := value.(string)
	cleaned := strings.TrimSpace(text)
	switch {
	case !present:
		wrong.add(requestbody.FieldRequired, at, name)
	case !isText:
		wrong.add(requestbody.NotAString, at, name)
	case cleaned == "":
		wrong.add(blankText, at, name)
	case strings.ContainsRune(cleaned, 0):
		wrong.add(nulRefused, at, name)
	case utf8.RuneCountInString(cleaned) > maxCharacters:
		wrong.add(atMostCharacters(maxCharacters), at, name)
	}
	return cleaned
}

// readDuration is the target length as a range of seconds.
//
// Briefs stored before lengths were kept in seconds hold `min_minutes` and
// `max_minutes` instead; out of the column those still read, as sixty times
// each.
func readDuration(wrong *faults, parent *pythonjson.Object, fromColumn bool, at []string) *pythonjson.Object {
	value, present := parent.Get("duration")
	if !present {
		wrong.add(requestbody.FieldRequired, at, "duration")
		return nil
	}
	written, isObject := value.(*pythonjson.Object)
	if !isObject {
		wrong.add(notAnObject, at, "duration")
		return nil
	}
	at = append(append([]string{}, at...), "duration")
	names, scale := []string{"min_seconds", "max_seconds"}, int64(1)
	if _, inMinutes := written.Get("min_minutes"); inMinutes && fromColumn {
		names, scale = []string{"min_minutes", "max_minutes"}, 60
	}
	before := len(wrong.found)
	refuseOtherKeys(wrong, written, at, names...)
	bounds := make([]int64, 0, 2)
	for _, name := range names {
		kept, present := written.Get(name)
		number, isNumber := wholeNumber(kept)
		// Checked before it is scaled, so the product cannot overflow.
		seconds := number * scale
		switch {
		case !present:
			wrong.add(requestbody.FieldRequired, at, name)
		case !isNumber:
			wrong.add(notAWholeNumber, at, name)
		case number < 0 || seconds < minDurationSeconds:
			wrong.add("Input should be greater than or equal to "+strconv.Itoa(minDurationSeconds)+" seconds", at, name)
		case number > maxDurationSeconds || seconds > maxDurationSeconds:
			wrong.add("Input should be less than or equal to "+strconv.Itoa(maxDurationSeconds)+" seconds", at, name)
		default:
			bounds = append(bounds, seconds)
		}
	}
	if len(wrong.found) > before {
		return nil
	}
	if bounds[1] < bounds[0] {
		wrong.add(durationReversed, at, names[1])
		return nil
	}
	rendered := pythonjson.NewObject()
	rendered.Set("min_seconds", bounds[0])
	rendered.Set("max_seconds", bounds[1])
	return rendered
}

// wholeNumber is a JSON number that is a whole one: `3`, and `3.0` too,
// since JSON has one number type and jsonb keeps the spelling it was given.
// Text and truths are not numbers.
func wholeNumber(value any) (int64, bool) {
	switch held := value.(type) {
	case pythonjson.RawInt:
		number, err := strconv.ParseInt(string(held), 10, 64)
		return number, err == nil
	case float64:
		// Inside the range a float holds every whole number of exactly.
		if held != math.Trunc(held) || math.Abs(held) > 1<<53 {
			return 0, false
		}
		return int64(held), true
	}
	return 0, false
}

// onlyKeys reports whether every key the object holds is a declared one. An
// undeclared key means the brief was written by something this code does not
// know, and guessing at half of it is worse than saying so.
func onlyKeys(object *pythonjson.Object, declared ...string) bool {
	for _, key := range object.Keys() {
		if !oneOf(key, declared) {
			return false
		}
	}
	return true
}

// refuseOtherKeys adds a fault for each key the object holds that is not a
// declared one.
func refuseOtherKeys(wrong *faults, object *pythonjson.Object, at []string, declared ...string) {
	for _, key := range object.Keys() {
		if !oneOf(key, declared) {
			wrong.add(extraKey, at, key)
		}
	}
}

func oneOf(text string, options []string) bool {
	for _, option := range options {
		if text == option {
			return true
		}
	}
	return false
}
