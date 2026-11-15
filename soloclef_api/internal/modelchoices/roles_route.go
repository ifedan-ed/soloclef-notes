package modelchoices

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// RolesRoute serves the model that looks at pictures, as the settings
// screen shows it:
//
//	GET  -> {"vision": {selected, model, available, default, models},
//	         "image":  {...}}
//	PUT  {"vision": "<name>" | null}
//
// "image" is answered only for the phone builds already installed, which
// require it (installedbuilds, model-roles-image): nothing is drawn by a
// model any more (figures and infographics are drawn by code), so its
// answer is always "no model, none to choose", and a PUT naming one is
// refused unless it clears it, which changes nothing.
//
// A body that names nothing is refused, because a write that changes
// nothing is a mistake nobody noticed.
type RolesRoute struct {
	Choices Choices
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

// Register adds the two operations.
func (r RolesRoute) Register(api huma.API) {
	for _, operation := range []struct {
		id, method, summary string
		serve               func(huma.Context)
	}{
		{"read-model-roles", http.MethodGet, "The caller's vision model", r.read},
		{"choose-model-roles", http.MethodPut, "Choose the caller's vision model", r.choose},
	} {
		serve := operation.serve
		huma.Register(api, huma.Operation{
			OperationID: operation.id, Method: operation.method, Path: RolesPath,
			Summary: operation.summary, Tags: []string{"model choices"},
		}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: serve}, nil
		})
	}
}

func (r RolesRoute) read(ctx huma.Context) {
	request := begin(ctx, r.Errors, r.Logger, "an account's model roles could not be read")
	caller, ok := request.caller(false)
	if !ok {
		return
	}
	r.answer(request, caller.UserID)
}

// imageChoiceNone is the "image" answer for the installed builds: nothing
// chosen, no default, nothing to choose.
// installed-builds: model-roles-image
var imageChoiceNone = answer{Models: []ownmodels.MenuEntry{}}

func (r RolesRoute) answer(request request, userID string) {
	looked, err := r.Choices.Look(request.ctx.Context(), userID, Vision)
	if err != nil {
		request.refuse("could not read the models", err)
		return
	}
	request.send(map[string]answer{"vision": answerOf(looked[Vision]), "image": imageChoiceNone})
}

func (r RolesRoute) choose(ctx huma.Context) {
	request := begin(ctx, r.Errors, r.Logger, "an account's model roles could not be chosen")
	caller, ok := request.caller(true)
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(requestbody.Buffered(ctx.Context()), &body); err != nil {
		request.invalid("vision", "The body must be a JSON object naming vision")
		return
	}
	// installed-builds: model-roles-image
	if raw, present := body["image"]; present {
		// An installed build's image picker can only clear it (its menu is
		// always empty); a name is refused, as there is nothing to choose.
		if _, cleared, readable := readName(raw); !readable || !cleared {
			request.invalid("image", "Pictures are drawn by the application; there is no image model to choose")
			return
		}
		delete(body, "image")
		if len(body) == 0 {
			r.answer(request, caller.UserID)
			return
		}
	}
	raw, present := body["vision"]
	if !present {
		request.invalid("vision", "Name vision: a model's name, or null for the default")
		return
	}
	// The name is held to its list before anything is stored.
	looked, err := r.Choices.Look(ctx.Context(), caller.UserID, Vision)
	if err != nil {
		request.refuse("could not read the proxy's lists", err)
		return
	}
	name, cleared, readable := readName(raw)
	if !readable {
		request.invalid("vision", "vision must be a model's name, or null for the default")
		return
	}
	if !cleared && !slices.Contains(looked[Vision].Models, name) {
		request.invalid("vision", "The model "+name+" is not offered for this. Choose one from the list.")
		return
	}
	if !store(request, r.Choices, caller.UserID, Vision, "vision", raw) {
		return
	}
	r.answer(request, caller.UserID)
}
