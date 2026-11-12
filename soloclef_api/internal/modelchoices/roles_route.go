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
)

// RolesRoute serves the two picture models together, as the settings
// screen shows them:
//
//	GET  -> {"vision": {selected, model, available, default, models},
//	         "image":  {...}}
//	PUT  {"vision": "<name>" | null, "image": "<name>" | null}
//
// A PUT changes the jobs it names and leaves the other alone; a body that
// names neither is refused, because a write that changes nothing is a
// mistake nobody noticed. Both names are checked before either is stored,
// so a body with one good name and one bad one changes nothing.
type RolesRoute struct {
	Choices Choices
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

// roles are the body's fields and the purpose each one sets.
var roles = []struct {
	field   string
	purpose Purpose
}{{"vision", Vision}, {"image", Image}}

// Register adds the two operations.
func (r RolesRoute) Register(api huma.API) {
	for _, operation := range []struct {
		id, method, summary string
		serve               func(huma.Context)
	}{
		{"read-model-roles", http.MethodGet, "The caller's vision and image models", r.read},
		{"choose-model-roles", http.MethodPut, "Choose the caller's vision or image model", r.choose},
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

func (r RolesRoute) answer(request request, userID string) {
	looked, err := r.Choices.Look(request.ctx.Context(), userID, Vision, Image)
	if err != nil {
		request.refuse("could not read the models", err)
		return
	}
	request.send(map[string]answer{"vision": answerOf(looked[Vision]), "image": answerOf(looked[Image])})
}

func (r RolesRoute) choose(ctx huma.Context) {
	request := begin(ctx, r.Errors, r.Logger, "an account's model roles could not be chosen")
	caller, ok := request.caller(true)
	if !ok {
		return
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(requestbody.Buffered(ctx.Context()), &body); err != nil {
		request.invalid("vision", "The body must be a JSON object naming vision, image or both")
		return
	}
	named := 0
	for _, role := range roles {
		if _, present := body[role.field]; present {
			named++
		}
	}
	if named == 0 {
		request.invalid("vision", "Name vision, image or both: a model's name, or null for the default")
		return
	}
	// Every name is read and held to its list before anything is stored.
	looked, err := r.Choices.Look(ctx.Context(), caller.UserID, Vision, Image)
	if err != nil {
		request.refuse("could not read the proxy's lists", err)
		return
	}
	for _, role := range roles {
		raw, present := body[role.field]
		if !present {
			continue
		}
		name, cleared, readable := readName(raw)
		if !readable {
			request.invalid(role.field, role.field+" must be a model's name, or null for the default")
			return
		}
		if !cleared && !slices.Contains(looked[role.purpose].Models, name) {
			request.invalid(role.field, "The model "+name+" is not offered for this. Choose one from the list.")
			return
		}
	}
	for _, role := range roles {
		if raw, present := body[role.field]; present && !store(request, r.Choices, caller.UserID, role.purpose, role.field, raw) {
			return
		}
	}
	r.answer(request, caller.UserID)
}
