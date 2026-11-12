package artifacts

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// This file is what the ten routes that read the file store have in common
// before any of them does its own work: who is asking and about which
// artifact. Whether they may is not decided here: every statement these
// routes run names the caller as the owner, so an artifact that is not the
// caller's is simply not found.

// largerThanAnyID is told to a caller whose id could not be one.
const largerThanAnyID = "Input is larger than any id this API issues"

// readID reads a path parameter that names a row (or a slide).
//
// Every id in this database is a bigint. A whole number wider than that
// names nothing, and there are two wrong ways to find that out: send it to
// Postgres, which fails the statement and turns a typo into a 500; or narrow
// it in Go, which silently answers about a different row. So it is refused
// here, as the bad parameter it is, in the same 422 as a parameter that is
// not a number at all. The three row-only routes above still answer the
// 500; they predate this.
func readID(text string, failures *[]errorenvelope.Failure, location ...string) int64 {
	value, failure, rangeErr := requestnumber.Parse(text, location...)
	switch {
	case failure != nil:
		*failures = append(*failures, *failure)
		return 0
	case rangeErr != nil:
		*failures = append(*failures, errorenvelope.Failure{Location: location, Message: largerThanAnyID})
		return 0
	}
	return value
}

// Gate is the two things every route here is given to refuse with. Each
// route type embeds it, so cmd/api builds one and hands the same value to
// all six.
type Gate struct {
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// admitted is a request that passed: who is asking and about what.
type admitted struct {
	caller     principal.Principal
	artifactID int64
	requestID  string
}

// admit reads who is asking and every parameter, and refuses every
// malformed one in a single 422 -- the artifact id in the path, then
// whatever `rest` reads (a second id, a query value, the body). A client
// fixing its request should not have to discover its mistakes one round trip
// at a time.
//
// There is no permission to check. The caller's id goes into the route's
// statements as the owner, so somebody else's artifact answers the same 404
// as an id that was never issued and no route says which ids exist.
//
// It answers the refusal itself and reports false; the route simply returns.
func (g Gate) admit(
	ctx huma.Context,
	response http.ResponseWriter,
	rest func(failures *[]errorenvelope.Failure),
) (admitted, bool) {
	requestID := requestcontext.ID(ctx.Context())
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the authentication gate. If the wiring is ever
		// wrong this is a 500, never an anonymous read of somebody's files.
		g.fail(ctx.Context(), response, requestID, noCaller, nil)
		return admitted{}, false
	}

	failures := make([]errorenvelope.Failure, 0, 3)
	artifactID := readID(ctx.Param("artifact_id"), &failures, "path", "artifact_id")
	if rest != nil {
		rest(&failures)
	}
	if len(failures) > 0 {
		g.Errors.WriteValidation(response, requestID, failures)
		return admitted{}, false
	}

	return admitted{caller: caller, artifactID: artifactID, requestID: requestID}, true
}

// fail logs what went wrong, with the request id a person reporting the
// error will quote, and answers the tier's sanitised 500. The cause is for
// the log only: an error from Postgres or the filesystem can carry a path, a
// key or a fragment of a row, and none of that is the client's to read.
//
// One "failure" is not one. A viewer that abandons a request -- every seek
// of a video, every panel closed while its manifest loads -- cancels the
// request's context, and whatever the route was waiting on (a query, a stat)
// then returns an error because of it. That used to be logged at ERROR as a
// failed route and answered with a 500 written to a closed connection. It is
// the client's decision, not a fault, and there is nobody left to answer:
// it is noted at INFO, with the cause kept in case it was something else,
// and nothing is written.
func (g Gate) fail(ctx context.Context, response http.ResponseWriter, requestID, reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	// Only a failure with a cause can be the client's leaving. One without
	// (a miswired route, a file in a store this tier cannot read) was found
	// by this code's own check and is true whoever is listening.
	if cause != nil && errors.Is(ctx.Err(), context.Canceled) {
		g.Logger.LogAttrs(ctx, slog.LevelInfo, "the client left before an artifact route answered", attributes...)
		return
	}
	g.Logger.LogAttrs(ctx, slog.LevelError, "an artifact route failed", attributes...)
	g.Errors.Write(response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, requestID)
}
