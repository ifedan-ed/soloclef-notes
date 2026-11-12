package nextcloudimport

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Refreshable is one document the caller can read again from Nextcloud,
// and the connection of theirs that can.
type Refreshable struct {
	DocumentID  int64 `json:"document_id"`
	ConnectorID int64 `json:"connector_id"`
}

// RefreshableRows is the database as the route uses it.
type RefreshableRows interface {
	// Refreshable is the account's Nextcloud documents, each with the
	// account's own connection for the same Nextcloud account on the
	// server named.
	Refreshable(ctx context.Context, userID, serverURL string) ([]Refreshable, error)
}

// RefreshableStore is RefreshableRows over the pool.
type RefreshableStore struct {
	Pool *pgxpool.Pool
}

func (s RefreshableStore) Refreshable(ctx context.Context, userID, serverURL string) ([]Refreshable, error) {
	rows, err := New(s.Pool).RefreshableDocuments(ctx, RefreshableDocumentsParams{UserID: userID, ServerUrl: serverURL})
	if err != nil {
		return nil, err
	}
	found := make([]Refreshable, len(rows))
	for index, row := range rows {
		found[index] = Refreshable{DocumentID: row.DocumentID, ConnectorID: row.ConnectorID}
	}
	return found, nil
}

// RefreshableRoute answers which of the caller's documents can be
// refreshed from Nextcloud, so the web offers "Refresh from Nextcloud" on
// exactly those.
//
// A file can be read again only through a connection to the account it
// came from. The Python route read every connection and every Nextcloud
// document of the workspace and matched them in a loop; this is one
// statement (queries.sql, RefreshableDocuments), and it is the caller's
// documents, not a workspace's.
//
// It is mounted in every deployment, unlike the import: the document list
// asks it whether or not there is a Nextcloud, and the true answer where
// there is none is that nothing can be refreshed.
type RefreshableRoute struct {
	Rows RefreshableRows
	// ServerURL is the deployment's Nextcloud (Credentials.ServerURL), or
	// "" for a deployment with none.
	ServerURL string
	Errors    errorenvelope.Writer
	Logger    *slog.Logger
}

func (r RefreshableRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "nextcloud-refreshable-documents", Method: http.MethodGet, Path: RefreshablePath,
		Summary: "The caller's documents that can be refreshed from Nextcloud", Tags: []string{"connectors"},
	}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r RefreshableRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	fail := func(reason string, cause error) {
		attributes := []slog.Attr{slog.String("request_id", requestID), slog.String("reason", reason)}
		if cause != nil {
			attributes = append(attributes, slog.String("error", cause.Error()))
		}
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "the refreshable-documents route failed", attributes...)
		r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
	}
	caller, signedIn := principal.FromContext(ctx.Context())
	if !signedIn {
		// Unreachable behind the gate; a 500 if the wiring is ever wrong.
		fail("the route ran without an authenticated caller", nil)
		return
	}
	found := []Refreshable{}
	if r.ServerURL != "" {
		var err error
		if found, err = r.Rows.Refreshable(ctx.Context(), caller.UserID, r.ServerURL); err != nil {
			fail("the documents could not be read", err)
			return
		}
	}
	body, err := json.Marshal(struct {
		Documents []Refreshable `json:"documents"`
	}{found})
	if err != nil {
		fail("the answer could not be encoded", err)
		return
	}
	pythonjson.Send(response, http.StatusOK, body)
}
