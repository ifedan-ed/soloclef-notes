package notebooks

import (
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// The two bodies, each read in its model's field order with
// internal/requestbody's readers, so that failures are reported in that
// order. Neither model forbids an extra field, so one is ignored and never
// read: nothing a body says can decide whose row is written.

// nulRefused is the message for a string holding U+0000.
const nulRefused = "String should not contain a NUL character"

// withoutNUL adds a failure for each named string that holds U+0000.
// Postgres stores no such character in text, so without this the body would
// validate, the statement would fail and the answer would be a 500; it is a
// 422 that names the field. A field that already failed is not looked at.
func withoutNUL(failures []errorenvelope.Failure, fields map[string]*string, order ...string) []errorenvelope.Failure {
	if len(failures) > 0 {
		return failures
	}
	for _, name := range order {
		if value := fields[name]; value != nil && strings.ContainsRune(*value, 0) {
			failures = append(failures, errorenvelope.Failure{
				Location: []string{requestbody.BodyRoot, name}, Message: nulRefused})
		}
	}
	return failures
}

// decoded is the first step of every reader: the body as an object, or the
// one model-level failure FastAPI reports instead.
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

// notebookCreation is a validated NotebookCreate.
type notebookCreation struct {
	Title string
	Icon  *string
}

// readNotebookCreation validates NotebookCreate:
//
//	title: str = Field(default="Untitled notebook", max_length=200)
//	icon: str | None = Field(default=None, max_length=16)
//
// with a validator on `title` that strips it and puts the default back when
// nothing is left. The length is checked *before* the validator runs, so 200
// letters inside two spaces is refused although what would be stored fits;
// and the icon has no validator, so "  " is stored as two spaces.
func readNotebookCreation(contentType string, body []byte) (notebookCreation, []errorenvelope.Failure, error) {
	object, failures, err := decoded(contentType, body)
	if err != nil || failures != nil {
		return notebookCreation{}, failures, err
	}
	model := requestbody.NewModel(object)
	title := model.TextOr("title", defaultTitle, 0, 200)
	icon := model.OptionalText("icon", 16)
	if failures := withoutNUL(model.Failures(), map[string]*string{"title": &title, "icon": icon}, "title", "icon"); len(failures) > 0 {
		return notebookCreation{}, failures, nil
	}
	// `value.strip() or "Untitled notebook"`. An absent title is the default
	// already, which strips to itself.
	title = pythontext.Strip(title)
	if title == "" {
		title = defaultTitle
	}
	return notebookCreation{Title: title, Icon: icon}, nil, nil
}

// templateEdit is a validated ReportTemplateUpdate.
type templateEdit struct {
	Title       *string
	Description *string
	Prompt      string
}

// readTemplateEdit validates ReportTemplateUpdate:
//
//	title: str | None = Field(default=None, max_length=200)
//	description: str | None = Field(default=None, max_length=500)
//	prompt: str = Field(min_length=1, max_length=4000)
//
// The prompt's minimum is of what was sent, not of what is left after the
// route strips it: three spaces pass, and the route then stores the seed's
// prompt instead (templates.go).
func readTemplateEdit(contentType string, body []byte) (templateEdit, []errorenvelope.Failure, error) {
	object, failures, err := decoded(contentType, body)
	if err != nil || failures != nil {
		return templateEdit{}, failures, err
	}
	model := requestbody.NewModel(object)
	edit := templateEdit{
		Title:       model.OptionalText("title", 200),
		Description: model.OptionalText("description", 500),
		Prompt:      model.Text("prompt", 1, 4000),
	}
	if failures := withoutNUL(model.Failures(), map[string]*string{"title": edit.Title, "description": edit.Description, "prompt": &edit.Prompt}, "title", "description", "prompt"); len(failures) > 0 {
		return templateEdit{}, failures, nil
	}
	return edit, nil, nil
}
