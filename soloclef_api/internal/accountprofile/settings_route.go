package accountprofile

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

const (
	settingsReadUnserved   = "the account's settings could not be served"
	settingsUpdateUnserved = "the account's settings could not be changed"
)

// SettingsStore reads and changes the caller's own settings.
type SettingsStore interface {
	AccountSettings(ctx context.Context, userID string) (Settings, error)
	ChangeSettings(ctx context.Context, userID string, changes SettingsChanges) (Settings, error)
}

// SettingsRoutes serves GET and PATCH /api/v1/users/me/settings: one resource
// for the three settings that are the account's own.
//
// The read is open to any signed-in caller, a key or an agent included, which
// by then has passed the gate and so can only be told the switch is on. The
// change is for a browser session only. That is what makes the API-access
// switch a control at all: a key cannot switch itself on (the gate has
// already refused it), and a leaked key cannot rewrite the instructions every
// later chat is given.
type SettingsRoutes struct {
	Settings SettingsStore
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

func (r SettingsRoutes) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-account-settings",
		Method:      http.MethodGet,
		Path:        SettingsReadPath,
		Summary:     "The caller's account settings",
		Description: "The account's standing chat instructions, whether answers " +
			"carry citations, and whether API keys and agents may act for it.",
		Tags: []string{"users"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveRead}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "update-account-settings",
		Method:      http.MethodPatch,
		Path:        SettingsUpdatePath,
		Summary:     "Change the caller's account settings",
		Description: "Changes the settings the body names and leaves the rest. " +
			"Only a browser session may call it.",
		Tags: []string{"users"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveUpdate}, nil
	})
}

func (r SettingsRoutes) serveRead(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID,
			"the route ran without an authenticated caller", nil, settingsReadUnserved)
		return
	}
	settings, err := r.Settings.AccountSettings(ctx.Context(), caller.UserID)
	if err != nil {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID,
			"could not read the caller's settings", err, settingsReadUnserved)
		return
	}
	r.send(ctx, response, requestID, settings, settingsReadUnserved)
}

func (r SettingsRoutes) serveUpdate(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	changes, failures, decodeErr := ReadSettingsChanges(
		ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID,
			"a body reached the route that was never buffered", decodeErr, settingsUpdateUnserved)
		return
	}

	// Who may ask comes before what was asked, so a key learns it may not
	// change settings and not what was wrong with its body.
	caller, ok := requireSession(ctx, r.Errors, r.Logger, response, requestID, settingsUpdateUnserved)
	if !ok {
		return
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	settings, err := r.Settings.ChangeSettings(ctx.Context(), caller.UserID, changes)
	if err != nil {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID,
			"could not change the caller's settings", err, settingsUpdateUnserved)
		return
	}
	r.send(ctx, response, requestID, settings, settingsUpdateUnserved)
}

func (r SettingsRoutes) send(ctx huma.Context, response http.ResponseWriter,
	requestID string, settings Settings, unserved string) {
	body, err := pythonjson.Marshal(BuildSettings(settings))
	if err != nil {
		writeFailure(ctx, r.Errors, r.Logger, response, requestID,
			"could not render the settings", err, unserved)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
