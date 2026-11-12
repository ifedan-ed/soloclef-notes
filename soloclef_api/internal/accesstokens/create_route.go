package accesstokens

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/database"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// CreatePath is where the settings page asks for a new key.
const CreatePath = "/api/v1/pats"

// CreatedEvent is the event a new key is counted by: how many people use the
// API and the MCP server.
const CreatedEvent = "pat_created"

func (r Routes) registerCreate(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-personal-access-token",
		Method:      http.MethodPost,
		Path:        CreatePath,
		Summary:     "Make a new API key for the caller",
		Description: "The whole key is in this answer and nowhere else: only " +
			"its hash and its first sixteen characters are stored.",
		Tags: []string{"pats"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serveCreate}, nil
	})
}

func (r Routes) serveCreate(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())

	requested, failures, decodeErr := ReadCreate(ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if decodeErr != nil {
		r.fail(ctx, response, requestID, "a body reached the route that should have been forwarded", decodeErr)
		return
	}
	// A browser session, before the body is looked at: a key can only be
	// made by a person signed in, never by another key (or an agent's
	// token), so that a leaked key cannot mint itself successors that
	// outlive its revocation. An API key is told 403 whatever its body.
	caller, ok := r.session(ctx, response, requestID)
	if !ok {
		return
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(response, requestID, failures)
		return
	}

	minted, err := MintKey()
	if err != nil {
		r.fail(ctx, response, requestID, "could not mint a key", err)
		return
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	created, err := database.Write(ctx.Context(), r.Pool,
		func(ctx context.Context, tx pgx.Tx, effects *database.Effects) (InsertTokenRow, error) {
			// Keyed by the caller's own id, from the session and never from
			// the body, so a key is always its maker's.
			row, err := New(tx).InsertToken(ctx, InsertTokenParams{
				UserID: caller.UserID, TokenHash: minted.Hash, TokenPrefix: minted.Prefix,
				Label: requested.Label, ExpiresAt: requested.ExpiresAt(now()),
			})
			if err != nil {
				return InsertTokenRow{}, err
			}
			// After the commit, so a key that was never stored is never
			// counted.
			effects.After(func() {
				r.Analytics.Record(caller, CreatedEvent, func(properties *pythonjson.Object) {
					properties.Set("pat_id", row.ID)
					properties.Set("has_expiry", row.ExpiresAt != nil)
				})
			})
			return row, nil
		})
	if err != nil {
		r.fail(ctx, response, requestID, "could not store the new key", err)
		return
	}

	body, err := pythonjson.Marshal(BuildCreated(created.ID, requested.Label, minted, created.ExpiresAt))
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the new key", err)
		return
	}
	// Never kept by a cache on the way: this answer is the only copy of the
	// key there will ever be.
	response.Header().Set("Cache-Control", "no-store")
	pythonjson.Send(response, http.StatusOK, body)
}

// BuildCreated renders a new key as the settings page reads it
// (createPatResponse): the whole key under "token", once.
func BuildCreated(id int64, label string, minted NewKey, expiresAt *time.Time) *pythonjson.Object {
	entry := pythonjson.NewObject()
	entry.Set("id", id)
	entry.Set("label", label)
	entry.Set("token", minted.Key)
	entry.Set("prefix", minted.Prefix)
	entry.Set("expires_at", momentOrNil(expiresAt))
	return entry
}
