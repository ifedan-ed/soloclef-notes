// Package zerosync answers zero-cache's query endpoint:
//
//	POST /internal/zero/query   ["transform", [{"id", "name", "args": [...]}]]
//	-> {"kind": "QueryResponse", "queries": [{"id", "name", "ast"}, ...], "userID"}
//
// Why it is here. The web app's live updates (notebooks' documents, the
// inbox, a chat's messages, a Studio job's progress) come from zero-cache,
// which turns each named query a browser asks for into a query on its
// replica by asking ZERO_QUERY_URL for the query's AST. That endpoint used
// to be a Next.js route handler (soloclef_web/app/api/zero/query). The web
// app is now static files with no server process, so the API answers it.
// It goes away with Zero itself, which another piece of work is removing.
//
// Why it is safe. The AST is the access rule: every query is narrowed to
// rows whose owner is the caller, and the caller is the one the credential
// gate resolved from the cookie (or bearer token) zero-cache forwards from
// the browser. Nothing a client sends names an account: the only inputs are
// a query's name, which picks one of nine fixed shapes, and an integer id,
// which narrows further inside the caller's own rows. So a client asking for
// another account's thread gets an AST that matches nothing.
//
// The nine shapes are soloclef_web/zero/queries, built here by hand rather
// than by Zero's TypeScript builder; testdata/handler_asts.json is that
// builder's own output for each, and the test holds every shape to it.
package zerosync

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Path is the route. It is under /internal because the stack's Caddy sends
// the API only /auth/*, /users/*, /verify-token and /api/v1/*
// (docker/proxy-go/routes.caddy): zero-cache reaches it by the
// API's container name, and a browser cannot reach it at all.
const Path = "/internal/zero/query"

// bodyMax bounds a request. A request is one connection's batch of named
// queries, each a name and an id: a few kilobytes even for a tab with every
// panel open. The bound is generous so a real batch never meets it.
const bodyMax = 64 << 10

// Handler answers zero-cache. It runs behind the credential gate, which
// puts the caller in the request's context.
type Handler struct {
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// request is one named query as zero-cache sends it.
type request struct {
	ID   string            `json:"id"`
	Name string            `json:"name"`
	Args []json.RawMessage `json:"args"`
}

// transformed is a query answered with its AST.
type transformed struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	AST  ast    `json:"ast"`
}

// refused is a query that could not be built: an unknown name or arguments
// of the wrong shape. zero-cache logs the message and tells the client the
// query failed; the rest of the batch is still answered.
type refused struct {
	Error   string `json:"error"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	Message string `json:"message"`
}

type queryResponse struct {
	Kind    string `json:"kind"`
	Queries []any  `json:"queries"`
	UserID  string `json:"userID"`
}

// transformFailed is the answer for a body that is not a transform request
// at all, in the shape zero-protocol's transformFailedBodySchema reads (the
// enum strings are zero-protocol's error-kind, -origin and -reason).
type transformFailed struct {
	Kind     string   `json:"kind"`
	Origin   string   `json:"origin"`
	Reason   string   `json:"reason"`
	Message  string   `json:"message"`
	QueryIDs []string `json:"queryIDs"`
}

func (h Handler) ServeHTTP(response http.ResponseWriter, httpRequest *http.Request) {
	caller, ok := principal.FromContext(httpRequest.Context())
	if !ok || caller.UserID == "" {
		// Only reachable if the gate was left out of the wiring: refuse
		// rather than build a query for nobody.
		h.Logger.ErrorContext(httpRequest.Context(), "the zero query route ran without a caller")
		h.Errors.Write(response, http.StatusInternalServerError,
			errorenvelope.GenericServerMessage, requestcontext.IDFrom(httpRequest))
		return
	}
	body, err := io.ReadAll(io.LimitReader(httpRequest.Body, bodyMax+1))
	if err != nil {
		h.Errors.Write(response, http.StatusBadRequest, "The request body did not arrive.", requestcontext.IDFrom(httpRequest))
		return
	}
	if len(body) > bodyMax {
		h.Errors.Write(response, http.StatusRequestEntityTooLarge, "The query batch is too large.", requestcontext.IDFrom(httpRequest))
		return
	}

	requests, err := parse(body)
	if err != nil {
		// Status 200, as Zero's own handler answers: the failure is in the
		// body, where zero-cache looks for it.
		writeJSON(response, transformFailed{
			Kind: "TransformFailed", Origin: "server", Reason: "parse",
			Message:  "Failed to parse query request: " + err.Error(),
			QueryIDs: []string{},
		})
		return
	}

	answers := make([]any, 0, len(requests))
	for _, query := range requests {
		built, err := build(query.Name, query.Args, caller.UserID)
		if err != nil {
			answers = append(answers, refused{Error: "app", ID: query.ID, Name: query.Name, Message: err.Error()})
			continue
		}
		answers = append(answers, transformed{ID: query.ID, Name: query.Name, AST: built})
	}
	writeJSON(response, queryResponse{Kind: "QueryResponse", Queries: answers, UserID: caller.UserID})
}

// parse reads ["transform", [{id, name, args}, ...]], and nothing else.
func parse(body []byte) ([]request, error) {
	var message []json.RawMessage
	if err := json.Unmarshal(body, &message); err != nil {
		return nil, errors.New("the body is not a JSON array")
	}
	if len(message) != 2 {
		return nil, errors.New(`expected ["transform", [...]]`)
	}
	var tag string
	if err := json.Unmarshal(message[0], &tag); err != nil || tag != "transform" {
		return nil, errors.New(`the message is not "transform"`)
	}
	var requests []request
	if err := json.Unmarshal(message[1], &requests); err != nil {
		return nil, errors.New("the queries are not a list of {id, name, args}")
	}
	for _, query := range requests {
		if query.ID == "" || query.Name == "" || query.Args == nil {
			return nil, errors.New("a query is missing its id, name or args")
		}
	}
	return requests, nil
}

func writeJSON(response http.ResponseWriter, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		// Every value here is made of strings, ints and slices of them.
		panic(fmt.Sprintf("zerosync: encoding the answer: %v", err))
	}
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write(body)
}

// integerArg is the one integer argument a by-id query takes, from the
// first (and only) argument object. zod's z.number() also takes 7.5; an id
// never is one, so a fraction is refused here rather than turned into a
// query that matches nothing.
func integerArg(args []json.RawMessage, key string) (int64, error) {
	if len(args) == 0 {
		return 0, fmt.Errorf("expected an object with %q", key)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args[0], &fields); err != nil || fields == nil {
		return 0, fmt.Errorf("expected an object with %q", key)
	}
	raw, present := fields[key]
	if !present {
		return 0, fmt.Errorf("%s: expected number, received undefined", key)
	}
	var number float64
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, fmt.Errorf("%s: expected number", key)
	}
	if number != math.Trunc(number) || math.Abs(number) > 1<<53 {
		return 0, fmt.Errorf("%s: expected an integer", key)
	}
	return int64(number), nil
}
