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

// PurposeRoute serves one job's model at one path: read it, and choose it.
//
//	GET  -> {selected, model, available, default, models}
//	PUT  {"model": "<a proxy name>" | null}  -> the same answer
//
// Whose model it is is the caller and nothing in the request.
type PurposeRoute struct {
	Path    string
	Purpose Purpose
	Choices Choices
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

// Register adds the two operations.
func (r PurposeRoute) Register(api huma.API) {
	for _, operation := range []struct {
		method, verb string
		serve        func(huma.Context)
	}{
		{http.MethodGet, "read", r.read},
		{http.MethodPut, "choose", r.choose},
	} {
		serve := operation.serve
		huma.Register(api, huma.Operation{
			OperationID: operation.verb + "-" + string(r.Purpose) + "-model",
			Method:      operation.method,
			Path:        r.Path,
			Summary:     "The caller's " + string(r.Purpose) + " model: " + operation.verb,
			Tags:        []string{"model choices"},
		}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: serve}, nil
		})
	}
}

func (r PurposeRoute) read(ctx huma.Context) {
	request := begin(ctx, r.Errors, r.Logger, "an account's "+string(r.Purpose)+" model could not be read")
	caller, ok := request.caller(false)
	if !ok {
		return
	}
	r.answer(request, caller.UserID)
}

func (r PurposeRoute) answer(request request, userID string) {
	looked, err := r.Choices.Look(request.ctx.Context(), userID, r.Purpose)
	if err != nil {
		request.refuse("could not read the model", err)
		return
	}
	request.send(answerOf(looked[r.Purpose]))
}

func (r PurposeRoute) choose(ctx huma.Context) {
	request := begin(ctx, r.Errors, r.Logger, "an account's "+string(r.Purpose)+" model could not be chosen")
	caller, ok := request.caller(true)
	if !ok {
		return
	}
	// A map, because a null and a missing field must be told apart: null
	// clears the choice, and a body that forgot the field must not.
	var body map[string]json.RawMessage
	if err := json.Unmarshal(requestbody.Buffered(ctx.Context()), &body); err != nil {
		request.invalid("model", "The body must be a JSON object with a model: a model's name, or null for the default")
		return
	}
	raw, present := body["model"]
	if !present {
		request.invalid("model", "model is required: a model's name, or null for the default")
		return
	}
	if !store(request, r.Choices, caller.UserID, r.Purpose, "model", raw) {
		return
	}
	r.answer(request, caller.UserID)
}

// store applies one field of a body to one purpose and reports whether the
// route should go on. The name is held to the proxy's list at the moment of
// choosing, so what is stored was offered when it was stored.
func store(request request, choices Choices, userID string, purpose Purpose, field string, raw json.RawMessage) bool {
	name, cleared, readable := readName(raw)
	if !readable {
		request.invalid(field, field+" must be a model's name, or null for the default")
		return false
	}
	ctx := request.ctx.Context()
	if cleared {
		if err := choices.Stored.Clear(ctx, userID, purpose); err != nil {
			request.fail("could not clear the model", err)
			return false
		}
		return true
	}
	looked, err := choices.Look(ctx, userID, purpose)
	if err != nil {
		request.refuse("could not read the proxy's list", err)
		return false
	}
	if !slices.Contains(looked[purpose].Models, name) {
		request.invalid(field, "The model "+name+" is not offered. Choose one from the list.")
		return false
	}
	if err := choices.Stored.Choose(ctx, userID, purpose, name); err != nil {
		request.fail("could not store the model", err)
		return false
	}
	return true
}
