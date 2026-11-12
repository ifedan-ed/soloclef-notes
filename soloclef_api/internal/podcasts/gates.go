package podcasts

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/voices"
)

// errAbsent is the one answer for a podcast the caller does not have: there
// is no such row, its id is one no row can have, or it is another account's.
// The three are told apart by nothing, so an id cannot be probed for.
var errAbsent = &refusal{status: http.StatusNotFound, message: podcastAbsent}

// podcastID reads the one path parameter every route here takes. A value
// that is not a whole number is the tier's usual 422; one that is a number
// too wide for the column (ids are bigint) names no podcast, which is
// `known == false` and a 404 without asking the database.
func podcastID(ctx huma.Context) (id int64, known bool, failure *errorenvelope.Failure) {
	value, failure, rangeErr := requestnumber.Parse(ctx.Param("podcast_id"), "path", "podcast_id")
	if failure != nil {
		return 0, false, failure
	}
	if rangeErr != nil {
		return 0, false, nil
	}
	return value, true, nil
}

// load is the caller's podcast, or errAbsent.
//
// There is no permission to check: the statement names the caller as the
// owner, so a podcast is found only by the account it belongs to. Whether an
// API key or an agent may act for the account at all was decided before the
// route ran (internal/principal).
func load(ctx context.Context, rows Reading, caller principal.Principal, id int64) (*Stored, error) {
	podcast, err := rows.Podcast(ctx, caller.UserID, id)
	if err != nil {
		return nil, err
	}
	if podcast == nil {
		return nil, errAbsent
	}
	return podcast, nil
}

// answerer writes one route's failures, so that each route says what went
// wrong and none of them says how a refusal or a 500 is written.
type answerer struct {
	ctx       huma.Context
	errors    errorenvelope.Writer
	logger    *slog.Logger
	response  http.ResponseWriter
	requestID string
	// what is the route's own sentence for the log.
	what string
}

// refuse answers whatever a unit of work ended with: a route's own refusal
// as the envelope it words, a body that does not validate as the 422 that
// lists what is wrong with it, a voice catalogue that could not answer as a
// 503, and anything else as the 500.
func (a answerer) refuse(err error, reason string) {
	var refused *refusal
	if errors.As(err, &refused) {
		a.errors.Write(a.response, refused.status, refused.message, a.requestID)
		return
	}
	var unacceptable *invalid
	if errors.As(err, &unacceptable) {
		a.errors.WriteValidation(a.response, a.requestID, unacceptable.failures)
		return
	}
	if message, isCatalogue := catalogueSentence(err); isCatalogue {
		a.unavailable(message, err)
		return
	}
	a.fail(reason, err)
}

// unavailable answers a 503 for something this tier depends on and cannot
// reach or was configured wrongly for. The caller is told a sentence; the
// cause, which can name settings and addresses, goes to the log only.
func (a answerer) unavailable(message string, cause error) {
	level := slog.LevelError
	if errors.Is(cause, voices.ErrNotConfigured) {
		// A deployment without a speech service is not a fault.
		level = slog.LevelInfo
	}
	a.logger.LogAttrs(a.ctx.Context(), level, a.what,
		slog.String("request_id", a.requestID),
		slog.String("error", cause.Error()))
	a.errors.Write(a.response, http.StatusServiceUnavailable, message, a.requestID)
}

// fail logs what went wrong and answers the tier's sanitised 500. The CORS
// headers are left on it, so the web app can read the envelope and show its
// request id.
func (a answerer) fail(reason string, cause error) {
	attributes := []slog.Attr{
		slog.String("request_id", a.requestID),
		slog.String("reason", reason),
	}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	a.logger.LogAttrs(a.ctx.Context(), slog.LevelError, a.what, attributes...)
	a.errors.Write(a.response, http.StatusInternalServerError,
		errorenvelope.GenericServerMessage, a.requestID)
}

// signedIn is the caller. Every route of this package is mounted behind the
// tier's authentication, the voices and the languages included -- which the
// Python tier served to anyone -- so there is always one.
func (a answerer) signedIn() (principal.Principal, bool) {
	caller, authenticated := principal.FromContext(a.ctx.Context())
	if !authenticated {
		// Unreachable behind the gate; a 500 rather than an unauthenticated
		// answer if the wiring is ever wrong.
		a.fail("the route ran without an authenticated caller", nil)
	}
	return caller, authenticated
}

// start is what every route about one podcast does first: the caller, then
// the id.
func (a answerer) start() (caller principal.Principal, id int64, proceed bool) {
	caller, authenticated := a.signedIn()
	if !authenticated {
		return caller, 0, false
	}
	id, known, failure := podcastID(a.ctx)
	if failure != nil {
		a.errors.WriteValidation(a.response, a.requestID, []errorenvelope.Failure{*failure})
		return caller, 0, false
	}
	if !known {
		a.refuse(errAbsent, "")
		return caller, 0, false
	}
	return caller, id, true
}
