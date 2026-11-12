// Package connectorparser is the parser a connection's files are read by:
// the choice the connector form shows and saves.
//
// A connection (Google Drive, Nextcloud, a watched folder) brings files in
// for as long as it exists, so its parser is a setting of the connection,
// `search_source_connectors.parser`, and not something asked on each
// import. The indexer copies it to each document it creates for the
// connection, and from then on the document's own column is what its jobs
// read.
//
// # Two routes, beside the connection's own
//
//	GET /api/v1/search-source-connectors/{connector_id}/parser
//	PUT /api/v1/search-source-connectors/{connector_id}/parser   {"parser": "datalab"}
//
// Both answer {"id": 12, "parser": "datalab"}; the parser is null on a
// connection nobody has chosen for.
//
// They are their own routes, not a field on the connection's read and
// update, because those two are split between the tiers: the update is the
// Python tier's (it validates each connector type's config), and it does
// not know this column. A parser saved through it would be dropped; a
// parser saved here is one statement on one column.
//
// # Whose connection
//
// A connection belongs to the account that made it, and only that account
// reads or changes its parser. Anybody else is told what they would be told
// about an id that names nothing.
package connectorparser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/danielgtaylor/huma/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// Path is both routes' path.
const Path = "/api/v1/search-source-connectors/{connector_id}/parser"

// bodyLimit bounds the PUT's body. The whole of a real one is
// {"parser":"unstructured"}.
const bodyLimit = 1 << 10

// Choice is a connection's parser. Parser is "" when none was chosen.
type Choice struct {
	ConnectorID int64
	Parser      string
}

// Rows is the database as the routes use it. Both answer nil for a
// connection that does not exist or is not the owner's.
type Rows interface {
	ParserOf(ctx context.Context, connectorID int64, owner string) (*Choice, error)
	SetParser(ctx context.Context, connectorID int64, owner, parser string) (*Choice, error)
}

// PostgresRows is Rows over the pool.
type PostgresRows struct {
	Pool *pgxpool.Pool
}

func (p PostgresRows) ParserOf(ctx context.Context, connectorID int64, owner string) (*Choice, error) {
	row, err := New(p.Pool).ParserOfOwnedConnection(ctx, ParserOfOwnedConnectionParams{ID: connectorID, Owner: owner})
	return choiceOf(row.ID, row.Parser, err)
}

func (p PostgresRows) SetParser(ctx context.Context, connectorID int64, owner, parser string) (*Choice, error) {
	row, err := New(p.Pool).SetParserOfOwnedConnection(ctx, SetParserOfOwnedConnectionParams{ID: connectorID, Owner: owner, Parser: parser})
	return choiceOf(row.ID, row.Parser, err)
}

func choiceOf(id int64, parser *string, err error) (*Choice, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	choice := &Choice{ConnectorID: id}
	if parser != nil {
		choice.Parser = *parser
	}
	return choice, nil
}

// Routes are the read and the save.
//
// cmd/api must not list the PUT in `withBodies`: the route reads its own
// small body, with its own limit.
type Routes struct {
	Rows    Rows
	Parsers parserchoice.Registry
	Errors  errorenvelope.Writer
	Logger  *slog.Logger
}

func (r Routes) Register(api huma.API) {
	for _, route := range []struct {
		id, method, summary string
		serve               func(huma.Context)
	}{
		{"read-connector-parser", http.MethodGet, "The parser a connection's files are read by", r.read},
		{"save-connector-parser", http.MethodPut, "Choose the parser a connection's files are read by", r.save},
	} {
		serve := route.serve
		huma.Register(api, huma.Operation{
			OperationID: route.id, Method: route.method, Path: Path, Summary: route.summary, Tags: []string{"connectors"},
		}, func(context.Context, *struct {
			ConnectorID string `path:"connector_id" doc:"The connection's id"`
		}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: serve}, nil
		})
	}
}

// asked is what both routes begin with: the caller and the connection's id.
// ok is false when the answer has already been written.
func (r Routes) asked(ctx huma.Context, response http.ResponseWriter, requestID string) (caller principal.Principal, connectorID int64, ok bool) {
	caller, signedIn := principal.FromContext(ctx.Context())
	if !signedIn {
		// Unreachable behind the gate; a 500 if the wiring is ever wrong.
		r.fail(ctx, response, requestID, "the route ran without an authenticated caller", nil)
		return caller, 0, false
	}
	connectorID, failure, rangeErr := requestnumber.Parse(ctx.Param("connector_id"), "path", "connector_id")
	if failure != nil {
		r.Errors.WriteValidation(response, requestID, []errorenvelope.Failure{*failure})
		return caller, 0, false
	}
	// Any positive bigint could be a connection's id; one that is nobody's
	// is the same 404 the lookup gives.
	if rangeErr != nil || connectorID < 1 {
		r.Errors.Write(response, http.StatusNotFound, notFound(connectorID), requestID)
		return caller, 0, false
	}
	return caller, connectorID, true
}

func (r Routes) read(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	caller, connectorID, ok := r.asked(ctx, response, requestID)
	if !ok {
		return
	}
	choice, err := r.Rows.ParserOf(ctx.Context(), connectorID, caller.UserID)
	r.answer(ctx, response, requestID, connectorID, choice, err)
}

func (r Routes) save(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	caller, connectorID, ok := r.asked(ctx, response, requestID)
	if !ok {
		return
	}

	// One byte past the limit, to tell a body of exactly the limit from a
	// longer one without reading whatever was sent.
	raw, err := io.ReadAll(io.LimitReader(ctx.BodyReader(), bodyLimit+1))
	if err != nil || len(raw) > bodyLimit {
		r.Errors.Write(response, http.StatusBadRequest, `Send {"parser": "<name>"}`, requestID)
		return
	}
	var body struct {
		Parser *string `json:"parser"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	// An unknown key is refused: a client that sends one believes it is
	// changing something this route does not change.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Parser == nil {
		r.Errors.Write(response, http.StatusBadRequest, `Send {"parser": "<name>"}`, requestID)
		return
	}
	// A connection's parser is always a named one. "" here would mean "the
	// default" on an upload; a setting that is saved says which.
	if *body.Parser == "" {
		r.Errors.Write(response, http.StatusBadRequest, "Name the parser to use for this connection", requestID)
		return
	}
	chosen, refused := parserchoice.Choose(r.Parsers, *body.Parser)
	if refused != nil {
		r.Errors.Write(response, parserchoice.Status, refused.Message, requestID)
		return
	}
	choice, err := r.Rows.SetParser(ctx.Context(), connectorID, caller.UserID, chosen.String())
	r.answer(ctx, response, requestID, connectorID, choice, err)
}

// answer writes a choice, or the 404 for a connection that is not the
// caller's, or the 500 for a read that failed.
func (r Routes) answer(ctx huma.Context, response http.ResponseWriter, requestID string, connectorID int64, choice *Choice, err error) {
	if err != nil {
		r.fail(ctx, response, requestID, "the connection's parser could not be read or saved", err)
		return
	}
	if choice == nil {
		r.Errors.Write(response, http.StatusNotFound, notFound(connectorID), requestID)
		return
	}
	shown := struct {
		ID     int64   `json:"id"`
		Parser *string `json:"parser"`
	}{ID: choice.ConnectorID}
	if choice.Parser != "" {
		shown.Parser = &choice.Parser
	}
	body, err := json.Marshal(shown)
	if err != nil {
		r.fail(ctx, response, requestID, "could not render the answer", err)
		return
	}
	header := response.Header()
	header.Set("Content-Type", "application/json")
	header.Set(requestcontext.Header, requestID)
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(body)
}

func notFound(connectorID int64) string {
	return "Connector with id " + strconv.FormatInt(connectorID, 10) + " not found"
}

func (r Routes) fail(ctx huma.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "a connection's parser route failed", attributes...)
	r.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
