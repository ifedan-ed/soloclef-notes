package prompts

import (
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// All six routes of this slice are gated the same way, so the gate lives here
// rather than once per route.

// requireSession is require_session_context, which is a *dependency* on all six
// routes: these are a person's own prompts, so an API key and an agent are
// refused in that tier's own words.
//
// Being a dependency decides when it runs. FastAPI solves a route's
// dependencies before it validates the route's own path parameters and body, so
// an API key with a body that would not validate is told 403 and never 422 —
// which is why the two write routes call this *before* reporting their
// failures. The one thing that happens earlier still is a body that will not
// decode, which FastAPI refuses before any dependency runs and this tier
// forwards (internal/requestbody).
func requireSession(ctx huma.Context, errors errorenvelope.Writer, logger *slog.Logger,
	response http.ResponseWriter, requestID, message string) (principal.Principal, bool) {
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// read of someone's prompts if the wiring is ever wrong.
		writeFailure(ctx, errors, logger, response, requestID,
			"the route ran without an authenticated caller", nil, message)
		return principal.Principal{}, false
	}
	if !caller.IsSession() {
		errors.Write(response, http.StatusForbidden, sessionRequired, requestID)
		return principal.Principal{}, false
	}
	return caller, true
}

// promptID reads the one path parameter three of the six routes share, as
// FastAPI coerces it (internal/requestnumber).
func promptID(ctx huma.Context) (int64, *errorenvelope.Failure, error) {
	return requestnumber.Parse(ctx.Param("prompt_id"), "path", "prompt_id")
}
