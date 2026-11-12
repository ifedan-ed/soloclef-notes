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
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Profiles reads an account's own row.
type Profiles interface {
	Profile(ctx context.Context, userID string) (Profile, error)
}

// ReadRoute serves GET /users/me (get_current_user_profile).
//
// It is the only route of this slice behind `get_auth_context` rather than
// `require_session_context`, so an API key and an agent token both reach it —
// and both are shown the account's address, its superuser flag.
// That is the account's own data read with a credential the account
// minted, and it is the reason the update and the delete beside it are
// session-only (accountprofile.go).
type ReadRoute struct {
	Profiles Profiles
	Errors   errorenvelope.Writer
	Logger   *slog.Logger
}

func (r ReadRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-own-account",
		Method:      http.MethodGet,
		Path:        ReadPath,
		Summary:     "The caller's own account",
		Description: "The same response as the Python tier's GET /users/me, " +
			"byte for byte.",
		Tags: []string{"users"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ReadRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return
	}

	profile, err := r.Profiles.Profile(ctx.Context(), caller.UserID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's account", err)
		return
	}
	body, err := pythonjson.Marshal(Build(profile))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the account", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r ReadRoute) fail(ctx huma.Context, response http.ResponseWriter,
	requestID, reason string, cause error) {
	writeFailure(ctx, r.Errors, r.Logger, response, requestID, reason, cause, readUnserved)
}
