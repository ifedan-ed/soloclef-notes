// Package uploadvision answers one question for the routes that bring files
// in (an upload, a folder upload, a Nextcloud import) and for the
// capabilities route that tells their dialogs what to offer: may this
// account ask for its documents' pictures to be described?
//
// It may when it has a vision model: its own choice (`user_models`, purpose
// vision, set at /users/me/model-roles), else the deployment's
// CHAT_VISION_MODEL. The indexer resolves the model the same way when it
// runs (soloclef_worker/internal/indexing, by soloclef_assistant/
// accountvision's rule), so a switch offered here is one the indexer can
// honour. The choice may be a proxy name or a model of the account's own
// connection ("own:<id>"): the indexer opens either.
package uploadvision

import (
	"context"
	"errors"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/modelchoices"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/accountvision"
)

// NoModel is the refusal, written for the person who switched vision on:
// neither the account nor the deployment has a vision model.
const NoModel = "No vision model is set for this account or on this server, so use_vision_llm cannot be switched on. A vision model is chosen in Settings, Models."

// Choices is where an account's model choices are read
// (modelchoices.Rows).
type Choices interface {
	Of(ctx context.Context, userID string) (map[modelchoices.Purpose]string, error)
}

// Vision answers for one deployment.
type Vision struct {
	Choices Choices
	// Default is the deployment's vision model, "" for none.
	Default string
}

// Check reports what is missing. Called once at start-up.
func (v Vision) Check() error {
	if v.Choices == nil {
		return errors.New("uploadvision: the account choices are required")
	}
	return nil
}

// Refusal is why the account may not ask for vision, or "" when it may.
func (v Vision) Refusal(ctx context.Context, userID string) (string, error) {
	chosen, err := v.Choices.Of(ctx, userID)
	if err != nil {
		return "", err
	}
	if accountvision.Name(chosen[modelchoices.Vision], v.Default) == "" {
		return NoModel, nil
	}
	return "", nil
}

// Available is Refusal as a yes or no, for the capabilities route.
func (v Vision) Available(ctx context.Context, userID string) (bool, error) {
	refusal, err := v.Refusal(ctx, userID)
	return refusal == "" && err == nil, err
}
