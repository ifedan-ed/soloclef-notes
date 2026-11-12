package connectorparser

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/parserchoice/parserchoicetest"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_ingest/parse"
)

const (
	owner    = "11111111-2222-4333-8444-555555555555"
	somebody = "99999999-2222-4333-8444-555555555555"
)

// connections is the table: each connection's owner and parser.
type connections struct {
	owners  map[int64]string
	parsers map[int64]string
	saves   int
}

func (c *connections) ParserOf(_ context.Context, connectorID int64, caller string) (*Choice, error) {
	if c.owners[connectorID] != caller {
		return nil, nil
	}
	return &Choice{ConnectorID: connectorID, Parser: c.parsers[connectorID]}, nil
}

func (c *connections) SetParser(_ context.Context, connectorID int64, caller, parser string) (*Choice, error) {
	if c.owners[connectorID] != caller {
		return nil, nil
	}
	c.parsers[connectorID] = parser
	c.saves++
	return &Choice{ConnectorID: connectorID, Parser: parser}, nil
}

func ask(t *testing.T, table *connections, as, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	api := humago.New(http.NewServeMux(), huma.DefaultConfig("test", "0"))
	// Local and Datalab are configured; LlamaParse and Unstructured are not.
	Routes{Rows: table, Parsers: parserchoicetest.Registry(t, parse.Local, parse.Datalab),
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}.Register(api)
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	request = request.WithContext(principal.WithPrincipal(request.Context(),
		principal.Principal{UserID: as, Method: principal.MethodSession}))
	recorder := httptest.NewRecorder()
	api.Adapter().ServeHTTP(recorder, request)
	return recorder
}

func newTable() *connections {
	return &connections{
		owners:  map[int64]string{12: owner, 13: owner, 14: somebody},
		parsers: map[int64]string{12: "local"}, // 13 has never been chosen for
	}
}

func TestReadingAConnectionsParser(t *testing.T) {
	for _, test := range []struct {
		name, target string
		status       int
		body         string
	}{
		{"a connection with a parser", "/api/v1/search-source-connectors/12/parser", 200, `{"id":12,"parser":"local"}`},
		{"a connection nobody has chosen for answers null, not a guess", "/api/v1/search-source-connectors/13/parser", 200, `{"id":13,"parser":null}`},
		{"somebody else's connection looks like no connection", "/api/v1/search-source-connectors/14/parser", 404, "Connector with id 14 not found"},
		{"no such connection", "/api/v1/search-source-connectors/99/parser", 404, "Connector with id 99 not found"},
		{"an id that is not a number", "/api/v1/search-source-connectors/twelve/parser", 422, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			answer := ask(t, newTable(), owner, http.MethodGet, test.target, "")
			if answer.Code != test.status || !strings.Contains(answer.Body.String(), test.body) {
				t.Errorf("answered %d %s, want %d with %s", answer.Code, answer.Body, test.status, test.body)
			}
		})
	}
}

func TestSavingAConnectionsParser(t *testing.T) {
	for _, test := range []struct {
		name, target, body string
		status             int
		says               string
		saved              string
	}{
		{name: "a configured parser is saved and answered", target: "/api/v1/search-source-connectors/13/parser",
			body: `{"parser": "datalab"}`, status: 200, says: `{"id":13,"parser":"datalab"}`, saved: "datalab"},
		{name: "a parser with no key is refused by name", target: "/api/v1/search-source-connectors/13/parser",
			body: `{"parser": "unstructured"}`, status: 400, says: "The unstructured parser is not configured on this server"},
		{name: "a word that is not a parser", target: "/api/v1/search-source-connectors/13/parser",
			body: `{"parser": "cloud"}`, status: 400, says: `is not a parser`},
		{name: "an empty name is not the default", target: "/api/v1/search-source-connectors/13/parser",
			body: `{"parser": ""}`, status: 400, says: "Name the parser"},
		{name: "null is not a choice", target: "/api/v1/search-source-connectors/13/parser",
			body: `{"parser": null}`, status: 400, says: "Send {"},
		{name: "a body with something else in it", target: "/api/v1/search-source-connectors/13/parser",
			body: `{"parser": "datalab", "name": "renamed"}`, status: 400, says: "Send {"},
		{name: "a body that is not JSON", target: "/api/v1/search-source-connectors/13/parser",
			body: `parser=datalab`, status: 400, says: "Send {"},
		{name: "a body far larger than the one field", target: "/api/v1/search-source-connectors/13/parser",
			body: `{"parser": "` + strings.Repeat("x", 4096) + `"}`, status: 400, says: "Send {"},
		{name: "somebody else's connection is not changed", target: "/api/v1/search-source-connectors/14/parser",
			body: `{"parser": "datalab"}`, status: 404, says: "Connector with id 14 not found"},
	} {
		t.Run(test.name, func(t *testing.T) {
			table := newTable()
			answer := ask(t, table, owner, http.MethodPut, test.target, test.body)
			if answer.Code != test.status || !strings.Contains(answer.Body.String(), test.says) {
				t.Fatalf("answered %d %s, want %d saying %q", answer.Code, answer.Body, test.status, test.says)
			}
			if test.saved != "" {
				if table.parsers[13] != test.saved {
					t.Errorf("saved %q", table.parsers[13])
				}
				return
			}
			if table.saves != 0 || table.parsers[13] != "" || table.parsers[14] != "" {
				t.Errorf("a refused save wrote something: %v", table.parsers)
			}
		})
	}
}
