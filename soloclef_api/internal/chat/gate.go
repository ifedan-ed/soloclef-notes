package chat

import (
	"context"
	"encoding/json"
	"errors"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/assembly"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestnumber"
)

// refusal is a route's own "no": a status and a sentence, and for the
// refusals the apps branch on, a code.
type refusal struct {
	status  int
	message string
	code    string
	// cause is what went wrong underneath, when something did. It is for
	// the log only: the reader gets the message.
	cause error
}

func (r *refusal) Error() string { return r.message }

var errThreadAbsent = &refusal{status: http.StatusNotFound, message: threadAbsent}

// authorise is the thread, if it is the caller's.
//
// That is the whole access rule, and it is one statement: the thread by its
// id and the caller's account. A thread of another account, a deleted one
// and one that never existed are the same "Thread not found", so asking
// tells nobody anything about anybody else's chats. Whether an API key or
// an agent may act for the account at all was decided before the route ran
// (internal/principal).
func (s *Service) authorise(ctx context.Context, caller principal.Principal, threadID int64) (*Thread, error) {
	// No thread has an id below one; asking would be a wasted round trip.
	if threadID < 1 {
		return nil, errThreadAbsent
	}
	thread, err := s.Rows.Thread(ctx, caller.UserID, threadID)
	if err != nil {
		return nil, err
	}
	if thread == nil {
		return nil, errThreadAbsent
	}
	return thread, nil
}

// answerer writes one request's failures.
type answerer struct {
	ctx       huma.Context
	errors    errorenvelope.Writer
	logger    *slog.Logger
	response  http.ResponseWriter
	requestID string
	// what is the route's own sentence for the log.
	what string
}

func newAnswerer(ctx huma.Context, response http.ResponseWriter, errors errorenvelope.Writer, logger *slog.Logger, what string) answerer {
	return answerer{ctx: ctx, errors: errors, logger: logger, response: response, requestID: requestcontext.ID(ctx.Context()), what: what}
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

// threadID reads the path's thread id. One that is not a whole number is
// the tier's usual 422.
func (a answerer) threadID() (int64, bool) {
	id, failure, rangeErr := requestnumber.Parse(a.ctx.Param("thread_id"), "path", "thread_id")
	if failure != nil {
		a.errors.WriteValidation(a.response, a.requestID, []errorenvelope.Failure{*failure})
		return 0, false
	}
	if rangeErr != nil {
		// A number no thread can have: authorise answers "not found".
		return 0, true
	}
	return id, true
}

// refuse answers whatever a unit of work ended with: a refusal as worded,
// anything else as the tier's 500.
func (a answerer) refuse(err error, reason string) {
	// A turn whose model is gone or cannot be asked about is the
	// assembly's to say (it has no HTTP of its own); here it becomes the
	// refusal the apps already know.
	if unanswerable := (*assembly.ModelError)(nil); errors.As(err, &unanswerable) {
		err = modelRefusal(unanswerable)
	}
	refused, isRefusal := err.(*refusal)
	if isRefusal && refused.cause != nil {
		// A refusal with a cause is a failure the reader is told about in
		// its own words (the model service is down); the cause still
		// belongs in the log.
		a.logger.LogAttrs(a.ctx.Context(), slog.LevelError, a.what,
			slog.String("request_id", a.requestID), slog.String("reason", refused.message), slog.String("error", refused.cause.Error()))
	}
	switch {
	case !isRefusal:
		a.fail(reason, err)
	case refused.code != "":
		a.coded(refused.status, refused.code, refused.message, nil)
	default:
		a.errors.Write(a.response, refused.status, refused.message, a.requestID)
	}
}

// coded writes a refusal the apps branch on.
//
// Their reader (soloclef_web/lib/chat/chat-request-errors.ts and its phone
// twin) takes the code and the sentence from a "detail" object, so that is
// where they are. The tier's own envelope is written beside it, so that
// everything that reads this tier's errors (the request id, the log line)
// reads this one like any other.
func (a answerer) coded(status int, code, message string, extra map[string]any) {
	detail := map[string]any{"errorCode": code, "message": message}
	for key, value := range extra {
		detail[key] = value
	}
	body, err := json.Marshal(map[string]any{
		"detail": detail,
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
