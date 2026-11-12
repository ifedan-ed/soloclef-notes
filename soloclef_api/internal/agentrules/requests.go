package agentrules

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// Requested is a validated body of a new rule.
type Requested struct {
	Permission string
	Pattern    string
	Action     string
	// ThreadID scopes the rule to one thread; nil is every chat of the
	// account.
	ThreadID *int64
}

// ReadRequest validates the body of a new rule.
//
// The fields are read in one order (permission, pattern, action, thread_id)
// whatever order the request wrote them, and every field is read even after
// one has failed, because a body with three faults is answered with all
// three.
//
// `pattern` has a default: absent is "*" and skips the length check, an
// explicit null is "Input should be a valid string", and an empty string is
// the minimum-length refusal (internal/requestbody.TextOr).
//
// There is no user_id. Whose the rule is comes from the request's principal;
// a body that carries one is read past, like any other field this route
// does not use, and never decides anything.
//
// threadOutOfRange is true for a thread id no thread can have: the route
// answers it as a thread that is not found.
func ReadRequest(contentType string, body []byte) (requested Requested, failures []errorenvelope.Failure, threadOutOfRange bool, err error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return Requested{}, nil, false, err
	}
	if failure != nil {
		return Requested{}, []errorenvelope.Failure{*failure}, false, nil
	}

	model := requestbody.NewModel(object)
	requested.Permission = model.Text("permission", textMinLength, textMaxLength)
	requested.Pattern = model.TextOr("pattern", DefaultPattern, textMinLength, textMaxLength)
	requested.Action = model.Choice("action", Actions)
	thread := model.OptionalNumber("thread_id")
	if !model.Valid() {
		return Requested{}, model.Failures(), false, nil
	}
	requested.ThreadID = thread.Value
	return requested, nil, thread.OutOfRange, nil
}

// Changes is a validated body of an update.
//
// An explicit null is the same request as an omission and writes nothing:
// neither column can hold a NULL, so there is nothing a null could mean.
// That is why these are plain pointers.
type Changes struct {
	Pattern *string
	Action  *string
}

// Mentions reports whether the body asked for any column to be written.
func (c Changes) Mentions() bool {
	return c.Pattern != nil || c.Action != nil
}

// ReadChanges validates the body of an update: pattern, then action.
func ReadChanges(contentType string, body []byte) (Changes, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return Changes{}, nil, err
	}
	if failure != nil {
		return Changes{}, []errorenvelope.Failure{*failure}, nil
	}

	model := requestbody.NewModel(object)
	pattern := model.MaybeText("pattern", textMinLength, textMaxLength)
	action := model.MaybeChoice("action", Actions)
	if !model.Valid() {
		return Changes{}, model.Failures(), nil
	}
	return Changes{Pattern: pattern.Value, Action: action.Value}, nil, nil
}

// StoredPermission is the permission as it is stored, and whether it is one:
// the text without the white space around it, made only of the characters a
// permission may have.
//
// The strip is internal/pythontext's, which also removes the control
// characters that count as white space (U+001C and its neighbours), so a
// permission pasted with one at its end is stored clean instead of refused.
func StoredPermission(requested string) (string, bool) {
	stripped := pythontext.Strip(requested)
	return stripped, PermissionPattern.MatchString(stripped)
}

// StoredPattern is the pattern as it is stored: without the white space
// around it, and the wildcard when nothing is left. A pattern of "   "
// passes the length check, because spaces are characters, and must not be
// stored as an empty pattern that matches nothing.
func StoredPattern(requested string) string {
	if stripped := pythontext.Strip(requested); stripped != "" {
		return stripped
	}
	return DefaultPattern
}
