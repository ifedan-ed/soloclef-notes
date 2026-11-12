package accountprofile

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// The settings resource. Unlike the profile it is under /api/v1, beside the
// account's memory (internal/markdownmemory).
const (
	SettingsReadPath   = "/api/v1/users/me/settings"
	SettingsUpdatePath = "/api/v1/users/me/settings"
)

// InstructionsMaxLength bounds the standing instructions, in characters. They
// are added to every chat's prompt, so the bound is what keeps one setting
// from eating the model's context on every turn.
const InstructionsMaxLength = 4000

// settingsFields are the three keys a change may name. Anything else is
// refused, so a client that misspells one is told and not quietly ignored.
var settingsFields = []string{"chat_instructions", "citations_enabled", "api_access_enabled"}

// notNullable is the refusal for a switch sent as null: a switch is on or
// off, and there is no third state to store.
const notNullable = "Input should be a valid boolean"

// Settings are the settings that are the account's own. There is one of each
// per account; nothing is set per notebook here (a notebook's own chat
// instructions live on the notebook and win inside it).
type Settings struct {
	// ChatInstructions is the person's standing instructions to every chat.
	// Empty means none.
	ChatInstructions string
	// CitationsEnabled is whether answers carry citations.
	CitationsEnabled bool
	// APIAccessEnabled is the account's one access control: while it is off,
	// no API key and no agent's token acts for the account
	// (principal.ErrAPIAccessOff). Off for a new account.
	APIAccessEnabled bool
}

// SettingsChanges is a validated change: nil means the request did not
// mention the setting, and it is left as it is.
type SettingsChanges struct {
	ChatInstructions *string
	CitationsEnabled *bool
	APIAccessEnabled *bool
}

// Mentions reports whether the request asked for anything to be written.
func (c SettingsChanges) Mentions() bool {
	return c.ChatInstructions != nil || c.CitationsEnabled != nil || c.APIAccessEnabled != nil
}

// ReadSettingsChanges validates a PATCH body.
//
// Every field is optional and an omitted one is left alone. A null for the
// instructions clears them, which is what a client clearing a text box sends;
// a null for a switch is refused. An undeclared key is refused too.
func ReadSettingsChanges(contentType string, body []byte) (SettingsChanges, []errorenvelope.Failure, error) {
	object, failure, err := requestbody.Decode(contentType, body)
	if err != nil {
		return SettingsChanges{}, nil, err
	}
	if failure != nil {
		return SettingsChanges{}, []errorenvelope.Failure{*failure}, nil
	}

	model := requestbody.NewModel(object)
	instructions := model.MaybeText("chat_instructions", 0, InstructionsMaxLength)
	citations := model.MaybeTruth("citations_enabled")
	apiAccess := model.MaybeTruth("api_access_enabled")
	model.RefuseExtras(settingsFields...)

	failures := model.Failures()
	for _, field := range []struct {
		name  string
		truth requestbody.MaybeTruth
	}{{"citations_enabled", citations}, {"api_access_enabled", apiAccess}} {
		// Present with no value is either an explicit null or a value the
		// model has already refused; only the null still needs saying.
		if value, _ := object.Get(field.name); field.truth.Present && field.truth.Value == nil && value == nil {
			failures = append(failures, errorenvelope.Failure{
				Location: []string{requestbody.BodyRoot, field.name},
				Message:  notNullable,
			})
		}
	}
	if len(failures) > 0 {
		return SettingsChanges{}, failures, nil
	}

	changes := SettingsChanges{CitationsEnabled: citations.Value, APIAccessEnabled: apiAccess.Value}
	if instructions.Present {
		cleared := ""
		changes.ChatInstructions = &cleared
		if instructions.Value != nil {
			changes.ChatInstructions = instructions.Value
		}
	}
	return changes, nil, nil
}

// AccountSettings reads the caller's settings.
func (s Store) AccountSettings(ctx context.Context, userID string) (Settings, error) {
	return settingsOf(New(s.Pool).AccountSettings(ctx, userID))
}

// ChangeSettings writes what the request mentioned and answers with the
// settings as they then stand. It is one statement, so it needs no
// transaction; a change that mentions nothing is a read and writes nothing.
func (s Store) ChangeSettings(ctx context.Context, userID string, changes SettingsChanges) (Settings, error) {
	if !changes.Mentions() {
		return s.AccountSettings(ctx, userID)
	}
	row, err := New(s.Pool).UpdateAccountSettings(ctx, UpdateAccountSettingsParams{
		ChatInstructions: changes.ChatInstructions,
		CitationsEnabled: changes.CitationsEnabled,
		ApiAccessEnabled: changes.APIAccessEnabled,
		ID:               userID,
	})
	return settingsOf(AccountSettingsRow(row), err)
}

func settingsOf(row AccountSettingsRow, err error) (Settings, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Settings{}, ErrAccountGone
	}
	if err != nil {
		return Settings{}, err
	}
	return Settings{
		ChatInstructions: row.ChatInstructions,
		CitationsEnabled: row.CitationsEnabled,
		APIAccessEnabled: row.ApiAccessEnabled,
	}, nil
}

// BuildSettings renders the resource. Both routes answer with it.
func BuildSettings(settings Settings) *pythonjson.Object {
	rendered := pythonjson.NewObject()
	rendered.Set("chat_instructions", settings.ChatInstructions)
	rendered.Set("citations_enabled", settings.CitationsEnabled)
	rendered.Set("api_access_enabled", settings.APIAccessEnabled)
	return rendered
}
