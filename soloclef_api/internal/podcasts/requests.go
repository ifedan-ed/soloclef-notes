package podcasts

import (
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// The two bodies this package reads: a new podcast, and an edited brief.
//
// Both are read strictly. A number has to be a JSON number -- not "2", not
// true -- and a key has to be one the body declares. The Python models
// coerced the first and ignored the second, which is how a request with a
// misspelt `speaker_count` made a two-speaker podcast and said nothing.
// Nothing first-party depended on either: the web app validates what it
// sends against the same shapes, and no app sends the create at all.

// bodyAt is where every failure of a body is located.
var bodyAt = []string{requestbody.BodyRoot}

// decodeBody is the body as an object, or the one failure that says it is
// not one. The error is a body the tier's body reader should have turned
// away before the route ran (internal/requestbody.Middleware).
func decodeBody(contentType string, body []byte) (*pythonjson.Object, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return nil, nil, err
	}
	if failure != nil {
		return nil, []errorenvelope.Failure{*failure}, nil
	}
	return object, nil, nil
}

// The defaults and bounds of a new podcast (app/podcasts/duration_limits.py
// and the create model). The lengths are a few seconds by default because a
// first take is meant to be cheap; the brief gate is where a real length is
// chosen.
const (
	defaultSpeakerCount = 2
	defaultMinSeconds   = 20
	defaultMaxSeconds   = 30
	maxTitleCharacters  = 500
)

// creation is a validated request for a new podcast.
type creation struct {
	Title string
	// Source is the text the episode will be drafted from.
	Source       string
	ThreadID     *int64
	SpeakerCount int64
	MinSeconds   int64
	MaxSeconds   int64
	// Focus is what the episode should dwell on, or nil.
	Focus *string
}

// readCreation validates the body of POST /podcasts. The body does not say
// whose podcast it is: that is the signed-in caller, always, and a key the
// body is not known to have is refused, so nothing in it can be mistaken
// for an owner.
//
//	title            text, 1 to 500 characters, not blank
//	source_content   text, not blank
//	thread_id        a whole number or null; optional
//	speaker_count    1 to 6; optional, 2
//	min_seconds      15 to 86400; optional, 20
//	max_seconds      15 to 86400 and at least min_seconds; optional, 30
//	focus            text of at most 2000 characters, or null; optional
func readCreation(body *pythonjson.Object) (creation, []errorenvelope.Failure) {
	var wrong faults
	refuseOtherKeys(&wrong, body, bodyAt, "title", "source_content",
		"thread_id", "speaker_count", "min_seconds", "max_seconds", "focus")

	request := creation{
		Title:  requiredText(&wrong, body, "title", maxTitleCharacters, bodyAt),
		Source: requiredText(&wrong, body, "source_content", math.MaxInt, bodyAt),
	}

	// Absent and null are both "no thread".
	if thread := body.GetOrNil("thread_id"); thread != nil {
		number, isNumber := wholeNumber(thread)
		if !isNumber {
			wrong.add(notAWholeNumber, bodyAt, "thread_id")
		} else {
			request.ThreadID = &number
		}
	}

	request.SpeakerCount = boundedNumber(&wrong, body, "speaker_count", defaultSpeakerCount, 1, maxSpeakers)
	before := len(wrong.found)
	request.MinSeconds = boundedNumber(&wrong, body, "min_seconds", defaultMinSeconds, minDurationSeconds, maxDurationSeconds)
	request.MaxSeconds = boundedNumber(&wrong, body, "max_seconds", defaultMaxSeconds, minDurationSeconds, maxDurationSeconds)
	if len(wrong.found) == before && request.MaxSeconds < request.MinSeconds {
		// Said here, about the request. The Python route found out while
		// building the brief, after the row was made, and answered a 500.
		wrong.add(durationReversed, bodyAt, "max_seconds")
	}

	if focus := body.GetOrNil("focus"); focus != nil {
		text, isText := focus.(string)
		switch {
		case !isText:
			wrong.add(requestbody.NotAString, bodyAt, "focus")
		case utf8.RuneCountInString(text) > maxFocusCharacters:
			wrong.add(atMostCharacters(maxFocusCharacters), bodyAt, "focus")
		case strings.ContainsRune(text, 0):
			wrong.add(nulRefused, bodyAt, "focus")
		default:
			// A focus of only spaces is none.
			if cleaned := strings.TrimSpace(text); cleaned != "" {
				request.Focus = &cleaned
			}
		}
	}
	return request, wrong.found
}

// boundedNumber is an optional whole number between two bounds, and its
// default when the body does not name it. Named, it has to be a number: a
// null is not "use the default", it is a mistake.
func boundedNumber(wrong *faults, body *pythonjson.Object, name string, fallback, least, most int64) int64 {
	value, present := body.Get(name)
	if !present {
		return fallback
	}
	number, isNumber := wholeNumber(value)
	switch {
	case !isNumber:
		wrong.add(notAWholeNumber, bodyAt, name)
	case number < least:
		wrong.add("Input should be greater than or equal to "+strconv.FormatInt(least, 10), bodyAt, name)
	case number > most:
		wrong.add("Input should be less than or equal to "+strconv.FormatInt(most, 10), bodyAt, name)
	default:
		return number
	}
	return fallback
}

// briefEdit is a validated request to replace a podcast's brief.
type briefEdit struct {
	Brief readBrief
	// ExpectedVersion is the version of the brief the editor was looking
	// at. The edit is refused if the brief has moved on since.
	ExpectedVersion int64
}

// readBriefEdit validates the body of PATCH /podcasts/{id}/spec:
//
//	spec               a whole brief (spec.go, parseBrief)
//	expected_version   a whole number, 1 or more
//
// The brief is checked here for being a brief. Whether its voices exist and
// speak its language is the roster's question, asked by the route.
func readBriefEdit(body *pythonjson.Object) (briefEdit, []errorenvelope.Failure) {
	var wrong faults
	refuseOtherKeys(&wrong, body, bodyAt, "spec", "expected_version")

	var edit briefEdit
	if spec, present := body.Get("spec"); !present {
		wrong.add(requestbody.FieldRequired, bodyAt, "spec")
	} else {
		read, found := parseBrief(spec, edited, []string{requestbody.BodyRoot, "spec"})
		edit.Brief = read
		wrong.found = append(wrong.found, found...)
	}

	version, present := body.Get("expected_version")
	switch number, isNumber := wholeNumber(version); {
	case !present:
		wrong.add(requestbody.FieldRequired, bodyAt, "expected_version")
	case !isNumber:
		wrong.add(notAWholeNumber, bodyAt, "expected_version")
	case number < 1:
		wrong.add("Input should be greater than or equal to 1", bodyAt, "expected_version")
	default:
		edit.ExpectedVersion = number
	}
	return edit, wrong.found
}
