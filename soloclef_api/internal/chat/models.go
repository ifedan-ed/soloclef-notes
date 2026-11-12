package chat

// Which model answers an account's chat.
//
// # The rule
//
// The deployment's models are reached through the model proxy and nothing
// else, so the models an account may pick from are the names the proxy
// itself lists (GET {ModelURL}/models), and what is stored is one of those
// names, as the proxy spells it. There is no translation between "the name
// in the picker" and "the name sent in the request": they are the same
// string.
//
// Beside them are the models of the account's own OpenAI-compatible
// connections, offered as "own:<n>" and called directly at the address the
// person gave (soloclef_assistant/ownmodels). Each entry of the menu says
// which it is, so the picker can mark the account's own.
//
// The choice is the account's, one row of `user_models` with purpose
// "chat", read and chosen at /api/v1/users/me/chat-model. "me" is the
// request's principal: no path, query or body names whose choice it is.
//
// # What is deliberately not here
//
//   - No list of models on the proxy key. The key may call whatever the
//     proxy offers; what an account may pick is the proxy's list, read
//     live, less the models the proxy itself says are for images, speech,
//     embeddings and the like (forChat).
//   - No second choice. An account whose model has been removed from the
//     proxy is told so, by name, before a word is generated. It is not
//     answered by some other model that happened to be configured: an
//     answer from a model nobody picked is a wrong answer that looks right.
//   - No guess for an account that has picked nothing. It uses the one
//     model the deployment names as its default (Settings.ChatModel), and
//     that name is held to the proxy's list like any other.

import (
	"context"
	"encoding/json"
	"errors"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/ownmodels"
)

// ChatModelPath is where the caller's chat model is read and chosen.
const ChatModelPath = "/api/v1/users/me/chat-model"

// The codes the apps already know for a model that cannot answer; each
// shows the sentence sent with it.
const (
	codeModelNotFound    = "MODEL_NOT_FOUND"
	codeModelUnavailable = "MODEL_PROVIDER_UNAVAILABLE"
)

// modelRefusal is a model that cannot answer, as the refusal the apps
// branch on.
func modelRefusal(e *assembly.ModelError) *refusal {
	if e.Unreachable {
		return &refusal{status: http.StatusServiceUnavailable, code: codeModelUnavailable, message: e.Message, cause: e.Cause}
	}
	return &refusal{status: http.StatusConflict, code: codeModelNotFound, message: e.Message}
}

// maxModelNameChars bounds a stored name; proxy model names are far shorter.
const maxModelNameChars = 255

// ModelChoices is where an account's choice is kept.
type ModelChoices interface {
	// ChatModel is the account's chosen name, or "" if it has chosen none.
	ChatModel(ctx context.Context, userID string) (string, error)
	// ChooseChatModel stores the account's choice; "" clears it.
	ChooseChatModel(ctx context.Context, userID, name string) error
}

// StoredChoices is ModelChoices in Postgres: the account's row of
// `user_models` with purpose "chat". No row means the deployment's default,
// so clearing a choice deletes the row and nothing stores a placeholder.
type StoredChoices struct {
	Pool *pgxpool.Pool
}

func (s StoredChoices) ChatModel(ctx context.Context, userID string) (string, error) {
	name, err := New(s.Pool).ChatModelOfAccount(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return name, err
}

func (s StoredChoices) ChooseChatModel(ctx context.Context, userID, name string) error {
	if name == "" {
		return New(s.Pool).ClearChatModel(ctx, userID)
	}
	return New(s.Pool).ChooseChatModel(ctx, ChooseChatModelParams{UserID: userID, Model: name})
}

// modelRemoved is the sentence for a model the proxy refused as unknown in
// the middle of a turn.
func modelRemoved(name string) string {
	return "The model answering your chats, " + name + ", is no longer offered. Choose another model in the model menu and send your message again."
}

// chatModelAnswer is what both routes answer: the choice and the list to
// choose from, as the picker draws them.
type chatModelAnswer struct {
	// Selected is the account's own choice, null when it has made none.
	Selected *string `json:"selected"`
	// Model is the model answering now and Available whether the proxy
	// still offers it; when it does not, the picker says so instead of
	// showing a name that would fail.
	Model     string `json:"model"`
	Available bool   `json:"available"`
	// Default is the model answering an account that has chosen none.
	Default string                `json:"default"`
	Models  []ownmodels.MenuEntry `json:"models"`
}

func chatModelAnswerOf(m *Models, chosen Chosen) chatModelAnswer {
	out := chatModelAnswer{Model: chosen.Name, Available: chosen.Offered, Default: m.Default, Models: ownmodels.MenuEntries(chosen.Models, chosen.Own)}
	if chosen.Selected != "" {
		out.Selected = &chosen.Selected
	}
	return out
}

// registerModelRoutes mounts the two routes of the model menu.
func (r Routes) registerModelRoutes(api huma.API) {
	for _, route := range []struct {
		id, method, summary string
		serve               func(huma.Context)
	}{
		{"chat-model-read", http.MethodGet, "The caller's chat model and the models they may choose", r.readChatModel},
		{"chat-model-choose", http.MethodPut, "Choose the caller's chat model", r.chooseChatModel},
	} {
		serve := route.serve
		huma.Register(api, huma.Operation{
			OperationID: route.id, Method: route.method, Path: ChatModelPath, Summary: route.summary, Tags: []string{"chat"},
		}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: serve}, nil
		})
	}
}

func (r Routes) readChatModel(ctx huma.Context) {
	s := r.Service
	response := humawriter.New(ctx)
	answer := newAnswerer(ctx, response, r.Errors, s.Logger, "an account's chat model could not be read")
	// "me" is the request's principal and nothing else.
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return
	}
	chosen, err := s.Models.Look(ctx.Context(), caller.UserID)
	if err != nil {
		answer.refuse(err, "could not read the chat model")
		return
	}
	writeJSON(response, http.StatusOK, chatModelAnswerOf(s.Models, chosen))
}

func (r Routes) chooseChatModel(ctx huma.Context) {
	s := r.Service
	response := humawriter.New(ctx)
	answer := newAnswerer(ctx, response, r.Errors, s.Logger, "an account's chat model could not be chosen")
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return
	}
	// "model" is a name, or null to go back to the server's default. The
	// field must be present: an empty body that cleared the choice would
	// be a mistake nobody noticed.
	// Read as a map because the two cases must be told apart: a null and a
	// missing field decode to the same thing in a struct.
	var body map[string]json.RawMessage
	if failures := decode(requestbody.Buffered(ctx.Context()), &body); failures != nil {
		r.Errors.WriteValidation(response, answer.requestID, failures)
		return
	}
	raw, present := body["model"]
	if !present {
		r.Errors.WriteValidation(response, answer.requestID, []errorenvelope.Failure{failure("model", "model is required: a model's name, or null for the server's default")})
		return
	}
	var name string
	if string(raw) != "null" {
		if err := json.Unmarshal(raw, &name); err != nil || strings.TrimSpace(name) == "" || len([]rune(name)) > maxModelNameChars {
			r.Errors.WriteValidation(response, answer.requestID, []errorenvelope.Failure{failure("model", "model must be a model's name, or null for the server's default")})
			return
		}
		// Held to the menu at the moment of choosing (the proxy's list and
		// the account's own models), so that what is stored was offered
		// when it was stored.
		withdrawn, err := s.Models.NotOffered(ctx.Context(), caller.UserID, []string{name})
		var unreachable *assembly.ModelError
		if errors.As(err, &unreachable) && unreachable.Unreachable {
			answer.refuse(&refusal{status: http.StatusServiceUnavailable, code: codeModelUnavailable, message: assembly.ModelsUnreachable, cause: err}, "")
			return
		}
		if err != nil {
			answer.fail("could not read the model menu", err)
			return
		}
		if len(withdrawn) > 0 {
			r.Errors.WriteValidation(response, answer.requestID, []errorenvelope.Failure{failure("model", "The model "+name+" is not offered. Choose one from the list.")})
			return
		}
	}
	if err := s.Choices.ChooseChatModel(ctx.Context(), caller.UserID, name); err != nil {
		answer.fail("could not store the chat model", err)
		return
	}
	chosen, err := s.Models.Look(ctx.Context(), caller.UserID)
	if err != nil {
		answer.refuse(err, "could not read the chat model back")
		return
	}
	writeJSON(response, http.StatusOK, chatModelAnswerOf(s.Models, chosen))
}
