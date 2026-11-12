package searchconnectors

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// The page. A page is cut by the database (queries.sql), so its two numbers
// are bounded here: an account has a handful of connections, and a limit
// nobody needs is not a statement worth sending.
const (
	defaultPageSize = 100
	maxPageSize     = 500
)

// ListRoute serves GET /api/v1/search-source-connectors: the caller's own
// connections, of every type, switched on or off.
type ListRoute struct {
	Rows   Rows
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

func (r ListRoute) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "read-search-source-connectors",
		Method:      http.MethodGet,
		Path:        ListPath,
		Summary:     "The caller's connections",
		Description: "One page of the account's own connections, by name. " +
			"`skip` (0 or more) and `limit` (1 to 500, 100 when absent) cut the page. " +
			"A stored credential is never answered.",
		Tags: []string{"connectors"},
	}, func(ctx context.Context, _ *struct{}) (*huma.StreamResponse, error) {
		return &huma.StreamResponse{Body: r.serve}, nil
	})
}

func (r ListRoute) serve(ctx huma.Context) {
	response := humawriter.New(ctx)
	requestID := requestcontext.ID(ctx.Context())
	failed := failures{ctx: ctx, errors: r.Errors, logger: r.Logger, response: response,
		requestID: requestID, message: "the connection list could not be served"}

	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		failed.caught(noCaller, nil)
		return
	}

	requested := ctx.URL()
	query := requested.Query()
	problems := make([]errorenvelope.Failure, 0, 2)
	skip := pageNumber(query, "skip", 0, 0, 0, &problems)
	limit := pageNumber(query, "limit", defaultPageSize, 1, maxPageSize, &problems)
	if len(problems) > 0 {
		r.Errors.WriteValidation(response, requestID, problems)
		return
	}

	connections, err := r.Rows.Connections(ctx.Context(), caller.UserID, skip, limit)
	if err != nil {
		failed.caught("could not read the account's connections", err)
		return
	}
	// Each config is reduced to what a read may show before anything is
	// written, so a row that cannot be shown fails the page whole instead of
	// leaving half a list on the wire.
	shown := make([]*pythonjson.Object, 0, len(connections))
	for _, connection := range connections {
		config, err := shownConfig(connection)
		if err != nil {
			failed.caught(rowRefused, err)
			return
		}
		shown = append(shown, config)
	}
	writeModel(failed, response, func() ([]byte, error) {
		items := make([][]byte, 0, len(connections))
		for index, connection := range connections {
			item, err := renderConnection(connection, shown[index])
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		return joinList(items), nil
	})
}

// pageNumber reads one of the page's two numbers: fallback when the query
// does not name it, and a refusal added to problems when it is not a whole
// number inside its bounds. most is 0 for a number with no upper bound of
// its own; it is then bounded by what the column type can carry.
func pageNumber(query url.Values, name string, fallback, least, most int64,
	problems *[]errorenvelope.Failure) int32 {
	const widest = 1<<31 - 1
	text, named := requestnumber.QueryValue(query, name)
	if !named {
		return int32(fallback)
	}
	value, failure, rangeErr := requestnumber.Parse(text, "query", name)
	if failure != nil {
		*problems = append(*problems, *failure)
		return 0
	}
	refuse := func(message string) int32 {
		*problems = append(*problems, errorenvelope.Failure{Location: []string{"query", name}, Message: message})
		return 0
	}
	if most == 0 {
		most = widest
	}
	switch {
	case rangeErr == nil && value < least:
		return refuse(requestbody.AtLeast(least))
	case rangeErr != nil || value > most:
		return refuse("Input should be less than or equal to " + strconv.FormatInt(most, 10))
	}
	return int32(value)
}
