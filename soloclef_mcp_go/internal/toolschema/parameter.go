// Package toolschema declares a tool's parameters once and derives from that
// declaration both the published input schema and the binding of a call's
// arguments.
//
// One declaration rather than two keeps the schema a client reads and the
// checks a call meets from drifting apart, which is the way a ported tool
// surface stops matching the one clients have configured.
package toolschema

// Kind is the type a parameter accepts, in the terms the published schema uses.
type Kind int

const (
	// Text is a JSON string.
	Text Kind = iota
	// Number is a JSON integer.
	Number
	// Flag is a JSON boolean.
	Flag
	// TextList is a JSON array of strings.
	TextList
)

// Parameter is one tool parameter: what a client sends, what the schema says
// about it, and what the server does when it is absent.
type Parameter struct {
	// Name is the wire name, as a client writes it in "arguments".
	Name string
	// Title is the human-readable field title the schema carries.
	Title string
	// Description is what a client shows a model.
	Description string
	Kind        Kind
	// Choices restricts a Text parameter to these values.
	Choices []string
	// Nullable marks a parameter that also accepts null, whose default is null.
	Nullable bool
	// Required marks a parameter with no default; its absence is a refusal.
	Required bool
	// Default is the value a handler sees when the parameter is absent: a
	// string, an int, a bool, or nil for a nullable parameter.
	Default any

	Minimum   *int
	Maximum   *int
	MinLength *int
}

// Signature is a tool's whole parameter list, in declaration order.
//
// ArgumentsTitle is the name of the model a refusal is reported against; the
// Python server derives it from the handler's function name, so it is given
// here rather than computed.
type Signature struct {
	ArgumentsTitle string
	Parameters     []Parameter
}

// Limit is a pointer to an int, for the constraint fields.
func Limit(value int) *int { return &value }
