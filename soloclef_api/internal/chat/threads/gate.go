package threads

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// refusal is a route's own "no": a status and a sentence, and for the one
// refusal the apps branch on, a code.
type refusal struct {
	status  int
	message string
	code    string
}

func (r *refusal) Error() string { return r.message }

var errThreadAbsent = &refusal{status: http.StatusNotFound, message: threadAbsent}

// authorise is the thread, if it is the caller's.
//
// That is the whole access rule, and it is one question to the database:
// the thread by its id and the caller's account. A thread of another
// account, one that was deleted and one that never existed are the same
// answer, "Thread not found", so nothing about anybody else's threads can
// be learned by asking. Whether an API key or an agent may act for the
// account at all was decided before the route ran (internal/principal).
func (s *Service) authorise(ctx context.Context, caller principal.Principal, threadID int64) (Thread, error) {
	// No thread has an id below one; asking the database would be a wasted
	// round trip.
	if threadID < 1 {
		return Thread{}, errThreadAbsent
	}
	thread, found, err := s.Store.Thread(ctx, caller.UserID, threadID)
	if err != nil {
		return Thread{}, err
	}
	if !found {
		return Thread{}, errThreadAbsent
	}
	return thread, nil
}

// answerer writes one request's answer and its failures.
type answerer struct {
	ctx       huma.Context
	errors    errorenvelope.Writer
	logger    *slog.Logger
	response  http.ResponseWriter
	requestID string
	// what is the route's own sentence for the log.
	what string
}

// signedIn is the caller. The routes are mounted behind the tier's
// authentication, so there is always one; a 500 if the wiring is ever wrong.
func (a answerer) signedIn() (principal.Principal, bool) {
	caller, authenticated := principal.FromContext(a.ctx.Context())
	if !authenticated {
		a.fail("the route ran without an authenticated caller", nil)
	}
	return caller, authenticated
}

// pathID reads a whole number from the path. One that is not a whole number
// is the tier's usual 422; one no row can have is 0, which names nothing.
func (a answerer) pathID(name string) (int64, bool) {
	id, failure, rangeErr := requestnumber.Parse(a.ctx.Param(name), "path", name)
	if failure != nil {
		a.errors.WriteValidation(a.response, a.requestID, []errorenvelope.Failure{*failure})
		return 0, false
	}
	if rangeErr != nil || id < 1 {
		return 0, true
	}
	return id, true
}

// invalid answers a request that cannot be read as asked.
func (a answerer) invalid(failures []errorenvelope.Failure) {
	a.errors.WriteValidation(a.response, a.requestID, failures)
}

// refuse answers whatever a unit of work ended with: a refusal as worded,
// anything else as the tier's 500.
func (a answerer) refuse(err error, reason string) {
	refused, isRefusal := err.(*refusal)
	switch {
	case !isRefusal:
		a.fail(reason, err)
	case refused.code != "":
		a.coded(refused.status, refused.code, refused.message)
	default:
		a.errors.Write(a.response, refused.status, refused.message, a.requestID)
	}
}

// coded writes a refusal the apps branch on: the code and the sentence in a
// "detail" object, where their reader looks
// (soloclef_web/lib/chat/chat-request-errors.ts), beside the tier's own
// envelope.
func (a answerer) coded(status int, code, message string) {
	body, err := json.Marshal(map[string]any{
		"detail": map[string]any{"errorCode": code, "message": message},
		"error": map[string]any{
			"code": errorenvelope.CodeForStatus(status), "message": message, "status": status, "request_id": a.requestID,
		},
	})
	if err != nil {
		a.fail("a refusal could not be encoded", err)
		return
	}
	header := a.response.Header()
	header.Set("Content-Type", "application/json")
	header.Set(requestcontext.Header, a.requestID)
	a.response.WriteHeader(status)
	_, _ = a.response.Write(body)
}

// fail logs what went wrong and answers the tier's sanitised 500.
func (a answerer) fail(reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", a.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	a.logger.LogAttrs(a.ctx.Context(), slog.LevelError, a.what, attributes...)
	a.errors.Write(a.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, a.requestID)
}

// send writes a JSON answer. HTML escaping is off: message content is
// stored JSON passed through as it is, and "<" in an answer is not a reason
// to rewrite it.
func (a answerer) send(status int, value any) {
	a.sendBody(status, func() ([]byte, error) { return encode(value) })
}

// sendBody writes an answer a route put together itself.
func (a answerer) sendBody(status int, build func() ([]byte, error)) {
	body, err := build()
	if err != nil {
		a.fail("the answer could not be encoded", err)
		return
	}
	header := a.response.Header()
	header.Set("Content-Type", "application/json")
	a.response.WriteHeader(status)
	_, _ = a.response.Write(body)
}
