package notebooks

import (
	"errors"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// The values a notebook's chat settings may take: the schema's CHECKs, said
// once more here so a bad value is a 422 that names its field and not a 500
// from the constraint.
var (
	chatGoals       = []string{"default", "learning_guide", "custom"}
	responseLengths = []string{"default", "longer", "shorter"}
)

// maxInstructions is the longest standing instruction a notebook's chat takes.
const maxInstructions = 2000

// notebookUpdate is a validated edit: each field says whether the request
// mentioned it, and only mentioned fields are written.
type notebookUpdate struct {
	title, icon, chatGoal, chatInstructions, responseLength requestbody.MaybeText
}

// readNotebookUpdate validates the edit. A title is stripped and must leave
// something; an icon or instruction that strips to nothing clears it; a
// null title, goal or length is no change, because none of them can be
// empty.
func readNotebookUpdate(contentType string, body []byte) (notebookUpdate, []errorenvelope.Failure, error) {
	object, failures, err := decoded(contentType, body)
	if err != nil || failures != nil {
		return notebookUpdate{}, failures, err
	}
	model := requestbody.NewModel(object)
	edit := notebookUpdate{
		title:            model.MaybeText("title", 0, 200),
		icon:             model.MaybeText("icon", 0, 16),
		chatGoal:         model.MaybeChoice("chat_goal", chatGoals),
		chatInstructions: model.MaybeText("chat_instructions", 0, maxInstructions),
		responseLength:   model.MaybeChoice("response_length", responseLengths),
	}
	failures = withoutNUL(model.Failures(), map[string]*string{
		"title": edit.title.Value, "icon": edit.icon.Value, "chat_instructions": edit.chatInstructions.Value,
	}, "title", "icon", "chat_instructions")
	if len(failures) > 0 {
		return notebookUpdate{}, failures, nil
	}
	if edit.title.Value != nil {
		title := strings.TrimSpace(*edit.title.Value)
		if title == "" {
			return notebookUpdate{}, []errorenvelope.Failure{{
				Location: []string{requestbody.BodyRoot, "title"}, Message: "Value error, title must not be blank"}}, nil
		}
		edit.title.Value = &title
	}
	edit.icon.Value = blankIsNothing(edit.icon.Value)
	edit.chatInstructions.Value = blankIsNothing(edit.chatInstructions.Value)
	return edit, nil, nil
}

// blankIsNothing strips a text and answers nil for one with nothing left.
func blankIsNothing(text *string) *string {
	if text == nil {
		return nil
	}
	stripped := strings.TrimSpace(*text)
	if stripped == "" {
		return nil
	}
	return &stripped
}

// RegisterNotebookUpdate serves the edit.
func (r Routes) RegisterNotebookUpdate(api huma.API) {
	register[notebookPath](api, r, http.MethodPatch, NotebookPath,
		"update-notebook", "Rename a notebook or change its chat settings", r.updateNotebook)
}

// updateNotebook writes the mentioned fields in one guarded statement and
// answers the notebook as it now is.
func (r Routes) updateNotebook(x *exchange) {
	id, ok := x.notebookParameter()
	if !ok {
		return
	}
	edit, failures, err := readNotebookUpdate(x.ctx.Header("Content-Type"), requestbody.Buffered(x.ctx.Context()))
	if err != nil {
		x.fail("a body reached the route that the body reader should have turned away", err)
		return
	}
	if len(failures) > 0 {
		x.invalid(failures)
		return
	}
	if id.nameless {
		x.notFound(notebookAbsent)
		return
	}
	// A null title, goal or length was read as "no change" above; only a
	// value is written to those NOT NULL columns.
	set := func(field requestbody.MaybeText) (bool, string) {
		if field.Value == nil {
			return false, ""
		}
		return true, *field.Value
	}
	setTitle, title := set(edit.title)
	setGoal, goal := set(edit.chatGoal)
	setLength, length := set(edit.responseLength)
	row, err := New(r.Database).UpdateNotebook(x.ctx.Context(), UpdateNotebookParams{
		SetTitle: setTitle, Title: title,
		SetIcon: edit.icon.Present, Icon: edit.icon.Value,
		SetChatGoal: setGoal, ChatGoal: goal,
		SetChatInstructions: edit.chatInstructions.Present, ChatInstructions: edit.chatInstructions.Value,
		SetResponseLength: setLength, ResponseLength: length,
		WrittenAt: r.now(),
		ID:        id.value,
		UserID:    x.caller.UserID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		x.notFound(notebookAbsent)
		return
	}
	if err != nil {
		x.fail("could not update the notebook", err)
		return
	}
	x.describeOne(Notebook(row), x.send)
}
