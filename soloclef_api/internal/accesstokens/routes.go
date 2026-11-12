package accesstokens

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/corsmirror"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/routeanalytics"
)

// The list and the revoke; the create is create_route.go's.
const (
	ListPath   = "/api/v1/pats"
	RevokePath = "/api/v1/pats/{pat_id}"
)

// sessionRequired is require_session_context's refusal, word for word: these
// routes manage the API keys, so an API key may not use them.
const sessionRequired = "This action requires an interactive session"

// RevokedEvent is the event the Python route captures after its commit.
const RevokedEvent = "pat_revoked"

// Tokens reads the caller's own keys.
type Tokens interface {
	Tokens(ctx context.Context, userID string) ([]Token, error)
}

// Routes serves the create, the list and the revoke.
type Routes struct {
	// Pool is taken as well as the store because the revoke opens a
	// transaction: the delete and the event it owes are one decision.
	Pool      *pgxpool.Pool
	Tokens    Tokens
	Analytics routeanalytics.Capture
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
	// Now is the clock a new key's expiry is counted from; nil is the
	// real one. Overridable for tests.
	Now func() time.Time
}

func (r Routes) Register(api huma.API) {
	r.registerCreate(api)
	huma.Register(api, huma.Operation{
		OperationID: "read-personal-access-tokens",
		Method:      http.MethodGet,
		Path:        ListPath,
		Summary:     "The caller's own API keys",
		Description: "The same response as the Python tier's GET /api/v1/pats, " +
			"byte for byte.",
		Tags: []string{"pats"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveList}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "revoke-personal-access-token",
		Method:      http.MethodDelete,
		Path:        RevokePath,
		Summary:     "Revoke one of the caller's own API keys",
		Description: "The same response as the Python tier's " +
			"DELETE /api/v1/pats/{pat_id}, and the same row gone and the same " +
			"PostHog event.",
		Tags: []string{"pats"},
	}, func(ctx context.Context, _ *struct {
		TokenID string `path:"pat_id" doc:"The key's id"`
	}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveRevoke}, nil
	})
}

func (r Routes) serveList(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, ok := r.session(ctx, response, requestID)
	if !ok {
		return
	}

	tokens, err := r.Tokens.Tokens(ctx.Context(), caller.UserID)
	if err != nil {
		r.fail(ctx, response, requestID, "could not read the caller's keys", err)
		return
	}
	body, err := pythonjson.Marshal(BuildList(tokens))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the key list", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}

func (r Routes) serveRevoke(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	caller, ok := r.session(ctx, response, requestID)
	if !ok {
		return
	}

	tokenID, failure, rangeErr := requestnumber.Parse(ctx.Param("pat_id"), "path", "pat_id")
	if failure != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{*failure})
		return
	}
	if rangeErr != nil {
		r.fail(ctx, response, requestID, "a parameter is outside the range a request can use", rangeErr)
		return
	}

	_, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, effects *database.Effects) (struct{}, error) {
			if err := Revoke(ctx, tx, caller.UserID, tokenID); err != nil {
				return struct{}{}, err
			}
			// Registered rather than captured: the Python route captures after
			// session.commit() returns, so a transaction that rolls back tells
			// PostHog nothing. The event fires whether or not a row matched,
			// as it does there — the route never looks.
			effects.After(func() {
				r.Analytics.Record(caller, RevokedEvent, func(properties *pythonjson.Object) {
					properties.Set("pat_id", tokenID)
				})
			})
			return struct{}{}, nil
		})
	if err != nil {
		r.fail(ctx, response, requestID, "could not revoke the key", err)
		return
	}

	pythonjson.SendNoContent(response)
}

// session is require_session_context: an authenticated caller whose credential
// is an interactive session, and the Python tier's own 403 for one whose is not.
func (r Routes) session(ctx huma.Context, response http.ResponseWriter, requestID string) (principal.Principal, bool) {
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// read of someone's keys if the wiring is ever wrong.
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return principal.Principal{}, false
	}
	if !caller.IsSession() {
		r.Errors.Write(response, http.StatusForbidden, sessionRequired, requestID)
		return principal.Principal{}, false
	}
	return caller, true
}

func (r Routes) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the API keys could not be served", attributes...)
	// app/routes/personal_access_tokens_routes.py catches nothing, so this is
	// a 500 Starlette's outermost handler writes — outside CORSMiddleware, and
	// so without the CORS headers (internal/corsmirror.Suppress). Reachable: the
	// revoke with a pat_id past int32 is one.
	corsmirror.Suppress(ctx.Context())
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
