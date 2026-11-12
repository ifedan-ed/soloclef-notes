package accesstokens

import (
	"strconv"
	"strings"
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// The bounds of a create (the web's createPatRequest says the same).
const (
	labelMaxLength = 120
	// maxExpiryDays is a hundred years: a key that should outlive that is a
	// key that should never expire, which is said by leaving the field out.
	maxExpiryDays = 36500
)

// The refusals of a create that the shared body reader has no words for.
const (
	labelBlank        = "The name must have at least one character that is not a space"
	expiryNotPositive = "Input should be greater than 0"
)

var expiryTooLong = "Input should be less than or equal to " + strconv.Itoa(maxExpiryDays)

// Requested is a validated create: the key's name, and how many days it
// lives (nil for a key that never expires).
type Requested struct {
	Label         string
	ExpiresInDays *int64
}

// ExpiresAt is when a key created at now stops working, or nil for never.
func (r Requested) ExpiresAt(now time.Time) *time.Time {
	if r.ExpiresInDays == nil {
		return nil
	}
	at := now.UTC().AddDate(0, 0, int(*r.ExpiresInDays))
	return &at
}

// ReadCreate validates the body of a create: {"label": ..., "expires_in_days":
// ... | null}. Every field is read even after one fails, so a body with two
// faults is answered with both.
func ReadCreate(contentType string, body []byte) (Requested, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return Requested{}, nil, err
	}
	if failure != nil {
		return Requested{}, []errorenvelope.Failure{*failure}, nil
	}
	model := requestbody.NewModel(object)
	var failures []errorenvelope.Failure
	refuse := func(field, message string) {
		failures = append(failures, errorenvelope.Failure{Location: []string{requestbody.BodyRoot, field}, Message: message})
	}

	// The name is stored trimmed, and a name of spaces alone is refused
	// rather than stored as an empty one nobody could tell apart in the list
	// (the Python route accepted it).
	label := strings.TrimSpace(model.Text("label", 1, labelMaxLength))
	failures = append(failures, model.Failures()...)
	if len(failures) == 0 && label == "" {
		refuse("label", labelBlank)
	}

	before := len(model.Failures())
	days := model.OptionalNumber("expires_in_days")
	failures = append(failures, model.Failures()[before:]...)
	switch {
	case days.OutOfRange:
		refuse("expires_in_days", expiryTooLong)
	case days.Value != nil && *days.Value < 1:
		refuse("expires_in_days", expiryNotPositive)
	case days.Value != nil && *days.Value > maxExpiryDays:
		refuse("expires_in_days", expiryTooLong)
	}
	if len(failures) > 0 {
		return Requested{}, failures, nil
	}
	return Requested{Label: label, ExpiresInDays: days.Value}, nil, nil
}
