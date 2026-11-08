package requestbody

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Model reads one pydantic model's fields out of a decoded body.
//
// Fields are read in the order they are declared on the model, because that is
// the order FastAPI reports their failures in — not the order they appear in the
// request. Every field is read even after one has failed, because a request with
// three bad fields is answered with all three.
type Model struct {
	body     *pythonjson.Object
	failures []errorenvelope.Failure
	// outOfRange is a value Python reads and the route then fails on — an
	// integer too wide for its column. It is not a validation failure on
	// either tier: the Python route answers it after the body has validated,
	// as its sanitised 500.
	outOfRange bool
}

// NewModel reads from a body that has already decoded to an object.
func NewModel(body *pythonjson.Object) *Model {
	return &Model{body: body}
}

// Failures is every failure in field order, empty when the body validated.
func (m *Model) Failures() []errorenvelope.Failure {
	return m.failures
}

// Valid reports whether every field read so far validated.
func (m *Model) Valid() bool {
	return len(m.failures) == 0
}

// OutOfRange reports whether a field held a number the request cannot use.
func (m *Model) OutOfRange() bool {
	return m.outOfRange
}

func (m *Model) reject(message string, path ...string) {
	m.failures = append(m.failures, errorenvelope.Failure{
		Location: append([]string{BodyRoot}, path...),
		Message:  message,
	})
}
