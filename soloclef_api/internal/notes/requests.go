package notes

import (
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// The bodies. Each is read with internal/requestbody's readers, every field
// even after one has failed, so a request with three bad fields hears about
// all three. No field names an owner: whose note is written is the caller's
// and nothing a body says.

// The words of a refused body field this package checks itself.
const (
	nulRefused       = "String should not contain a NUL character"
	textTooLong      = "The note's text is longer than a note can be (2 MB)"
	messageAlone     = "message_id needs the thread_id it belongs to"
	notebookRequired = "Field required"
	nullTitle        = "Input should be a valid string"
)

// Creation is a validated note to write.
type Creation struct {
	// Title is as sent, before trimming; "" when absent.
	Title string
	// Text is the note's Markdown; "" when absent.
	Text       string
	NotebookID *int64
	// ThreadID and MessageID name the answer a note is kept from.
	ThreadID  *int64
	MessageID *int64
	// OutOfRange is an id too wide for a bigint: it names nothing.
	OutOfRange bool
}

// ReadCreation validates the create's body:
//
//	title:       string, optional, at most MaxTitleLength characters
//	content:     string, optional (the Markdown)
//	notebook_id: integer or null, optional
//	thread_id:   integer or null, optional
//	message_id:  integer or null, optional; needs thread_id
func ReadCreation(contentType string, body []byte) (Creation, []errorenvelope.Failure, error) {
	object, failures, err := decoded(contentType, body)
	if err != nil || failures != nil {
		return Creation{}, failures, err
	}
	model := requestbody.NewModel(object)
	var creation Creation
	if title := model.OptionalText("title", MaxTitleLength); title != nil {
		creation.Title = *title
	}
	if text := model.OptionalText("content", 0); text != nil {
		creation.Text = *text
	}
	notebook := model.OptionalNumber("notebook_id")
	thread := model.OptionalNumber("thread_id")
	message := model.OptionalNumber("message_id")
	creation.NotebookID, creation.ThreadID, creation.MessageID = notebook.Value, thread.Value, message.Value
	creation.OutOfRange = notebook.OutOfRange || thread.OutOfRange || message.OutOfRange
	failures = model.Failures()
	if creation.MessageID != nil && creation.ThreadID == nil && !thread.OutOfRange {
		failures = append(failures, bodyFailure("message_id", messageAlone))
	}
	failures = checkText(failures, &creation.Title, &creation.Text)
	return creation, failures, nil
}

// Change is a validated save. A nil field is one the request left alone.
type Change struct {
	Title *string
	Text  *string
}

// ReadChange validates the save's body:
//
//	title:   string, optional, at most MaxTitleLength characters
//	content: string, optional (the Markdown)
//
// A null title is refused, as a title cannot be nothing; a null content is
// read as "", which empties the note.
func ReadChange(contentType string, body []byte) (Change, []errorenvelope.Failure, error) {
	object, failures, err := decoded(contentType, body)
	if err != nil || failures != nil {
		return Change{}, failures, err
	}
	model := requestbody.NewModel(object)
	var change Change
	title := model.MaybeText("title", 0, MaxTitleLength)
	text := model.MaybeText("content", 0, 0)
	failures = model.Failures()
	if title.Present && title.Value == nil {
		failures = append(failures, bodyFailure("title", nullTitle))
	}
	change.Title = title.Value
	if text.Present {
		empty := ""
		change.Text = &empty
		if text.Value != nil {
			change.Text = text.Value
		}
	}
	failures = checkText(failures, change.Title, change.Text)
	return change, failures, nil
}

// Filing is a validated move: the notebook to file the note in, or nil for
// none.
type Filing struct {
	NotebookID *int64
	OutOfRange bool
}

// ReadFiling validates the move's body: `notebook_id`, an integer or null,
// which must be there, so that an empty body is a mistake and not a quiet
// "file it nowhere".
func ReadFiling(contentType string, body []byte) (Filing, []errorenvelope.Failure, error) {
	object, failures, err := decoded(contentType, body)
	if err != nil || failures != nil {
		return Filing{}, failures, err
	}
	if _, present := object.Get("notebook_id"); !present {
		return Filing{}, []errorenvelope.Failure{bodyFailure("notebook_id", notebookRequired)}, nil
	}
	model := requestbody.NewModel(object)
	notebook := model.OptionalNumber("notebook_id")
	return Filing{NotebookID: notebook.Value, OutOfRange: notebook.OutOfRange}, model.Failures(), nil
}

// decoded is the first step of every reader: the body as an object, or the
// one failure that says it is not one.
func decoded(contentType string, body []byte) (*pythonjson.Object, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return nil, nil, err
	}
	if failure != nil {
		return nil, []errorenvelope.Failure{*failure}, nil
	}
	return object, nil, nil
}

// checkText refuses a text over MaxTextBytes and a NUL in either field:
// Postgres stores no NUL in text, so without this the body would validate
// and the statement fail, a 500 for what is the caller's mistake.
func checkText(failures []errorenvelope.Failure, title, text *string) []errorenvelope.Failure {
	if title != nil && strings.ContainsRune(*title, 0) {
		failures = append(failures, bodyFailure("title", nulRefused))
	}
	if text != nil {
		if len(*text) > MaxTextBytes {
			failures = append(failures, bodyFailure("content", textTooLong))
		} else if strings.ContainsRune(*text, 0) {
			failures = append(failures, bodyFailure("content", nulRefused))
		}
	}
	return failures
}

func bodyFailure(name, message string) errorenvelope.Failure {
	return errorenvelope.Failure{Location: []string{requestbody.BodyRoot, name}, Message: message}
}
