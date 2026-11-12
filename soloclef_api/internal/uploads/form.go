package uploads

import (
	"fmt"
	"io"
	"mime/multipart"
	"strings"
	"unicode"
	"unicode/utf8"
)

// refusal is a request this route will not carry out: a status and a
// sentence for the caller.
type refusal struct {
	status  int
	message string
}

func refuse(status int, format string, arguments ...any) *refusal {
	return &refusal{status: status, message: fmt.Sprintf(format, arguments...)}
}

// fields are the form's text fields, as sent.
type fields struct {
	// parser is the submitted name, or "" when the field was left out, in
	// which case the deployment's named default applies
	// (parserchoice.Choose).
	parser string
	vision bool
	// seen is which fields have arrived, so that one sent twice is refused
	// instead of the second quietly winning.
	seen map[string]bool
}

// take reads one text field into the form.
//
// Only the two fields the apps send are accepted, each once, each in one
// exact spelling. An unknown field is refused by name: a client that sends
// something this route does not read believes it is asking for something,
// and silence would let it go on believing. That includes a field naming
// whose documents these are to be: they are the caller's, and a form that
// says otherwise is refused, not obeyed and not ignored.
func (f *fields) take(part *multipart.Part) *refusal {
	name := part.FormName()
	if f.seen == nil {
		f.seen = map[string]bool{}
	}
	if f.seen[name] {
		return refuse(400, "The %s field was sent more than once", name)
	}
	f.seen[name] = true

	// One byte past the limit, to tell a value of exactly the limit from a
	// longer one without reading whatever was sent.
	raw, err := io.ReadAll(io.LimitReader(part, fieldLimit+1))
	if err != nil {
		return refuse(400, Interrupted)
	}
	if len(raw) > fieldLimit {
		return refuse(400, "The %s field is too long", name)
	}
	value := strings.TrimSpace(string(raw))

	switch name {
	case fieldParser:
		f.parser = value
	case fieldVision:
		switch value {
		case "true":
			f.vision = true
		case "false":
			f.vision = false
		default:
			return refuse(400, "use_vision_llm must be true or false")
		}
	default:
		return refuse(400, "This upload form has no %q field; it takes parser, use_vision_llm and files", name)
	}
	return nil
}

// nameLimit is the longest file name accepted, in bytes: what common file
// systems allow, and far more than a list row can show.
const nameLimit = 255

// uploadedName is the name of a file part, checked.
//
// mime/multipart has already reduced it to its last path element, so a name
// cannot carry a folder. What is refused here is a name the database or the
// document list could not hold as it is: empty, too long, not UTF-8, or
// carrying a control character (a NUL would fail the insert with an error
// that says nothing useful to the person who sent it).
func uploadedName(part *multipart.Part) (string, *refusal) {
	name := part.FileName()
	switch {
	case name == "" || name == "." || name == "/":
		return "", refuse(400, "A file was sent without a name")
	case len(name) > nameLimit:
		return "", refuse(400, "A file's name is longer than %d bytes", nameLimit)
	case !utf8.ValidString(name):
		return "", refuse(400, "A file's name is not valid UTF-8")
	}
	for _, letter := range name {
		if unicode.IsControl(letter) {
			return "", refuse(400, "A file's name contains a control character")
		}
	}
	return name, nil
}
