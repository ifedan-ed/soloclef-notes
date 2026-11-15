package mailconfirm

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/maildrafts"
)

// Guard is the send guard (*maildrafts.Guard).
type Guard interface {
	Confirm(ctx context.Context, ownerID, draftID string) (maildrafts.View, error)
	Send(ctx context.Context, ownerID, draftID, token, presentedHash string, acknowledged []string) (maildrafts.View, error)
	Discard(ctx context.Context, ownerID, draftID string) (maildrafts.View, error)
	Waiting(ctx context.Context, ownerID string) (maildrafts.WaitingList, error)
}

// Routes serves the four routes. Every field is required.
type Routes struct {
	Guard  Guard
	Errors errorenvelope.Writer
	Logger *slog.Logger
}

// Check reports what is missing. Called once at start-up.
func (r Routes) Check() error {
	if r.Guard == nil || r.Logger == nil {
		return errors.New("mailconfirm: Guard and Logger are required")
	}
	return nil
}

// Register adds the four operations.
func (r Routes) Register(api huma.API) {
	type draft struct {
		DraftID string `path:"draft_id" doc:"The draft's id"`
	}
	stream := func(serve func(huma.Context)) *huma.StreamResponse { return &huma.StreamResponse{Body: serve} }
	operation := func(id, path, summary, description string) huma.Operation {
		return huma.Operation{OperationID: id, Method: http.MethodPost, Path: path, Summary: summary,
			Description: description, Tags: []string{"mail"}}
	}
	huma.Register(api, operation("confirm-mail-draft", ConfirmationPath, "A mail draft and its go-ahead",
		"The draft as stored and, while it can be sent, a single-use go-ahead bound to exactly this content. A session only."),
		func(context.Context, *draft) (*huma.StreamResponse, error) { return stream(r.confirm), nil })
	huma.Register(api, operation("send-mail-draft", SendPath, "Send a confirmed mail draft",
		"Sends the stored draft if the go-ahead is valid, unspent, the caller's, the draft unchanged since it was shown, and every unusual point the go-ahead listed acknowledged. A session only."),
		func(context.Context, *draft) (*huma.StreamResponse, error) { return stream(r.send), nil })
	huma.Register(api, operation("discard-mail-draft", DiscardPath, "Discard a mail draft",
		"Drops a draft that is waiting. A session only."),
		func(context.Context, *draft) (*huma.StreamResponse, error) { return stream(r.discard), nil })
	huma.Register(api, huma.Operation{OperationID: "list-waiting-mail-drafts", Method: http.MethodGet, Path: WaitingPath,
		Summary: "The mail drafts still waiting", Tags: []string{"mail"},
		Description: "The account's drafts that can still be sent (or are being sent), newest first: each one's id, subject, mailbox and expiry. " +
			"Each is opened with its confirmation route, which issues its go-ahead. A session only."},
		func(context.Context, *struct{}) (*huma.StreamResponse, error) { return stream(r.waiting), nil })
}

// waiting lists the drafts still waiting. It is held to a session like the
// other three: the list names whom the person is writing to only by
// subject, but it is the way to a Send button, and a program holding the
// account's key has no business near one.
func (r Routes) waiting(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok {
		return
	}
	list, err := r.Guard.Waiting(ctx.Context(), c.caller.UserID)
	if err != nil {
		r.failed(c, "could not list the waiting drafts", err)
		return
	}
	rendered, err := json.Marshal(list)
	if err != nil {
		r.failed(c, "could not render the waiting drafts", err)
		return
	}
	// Drafts come and go by the minute; a cached list would offer one
	// already sent.
	c.response.Header().Set("Cache-Control", "no-store")
	pythonjson.Send(c.response, http.StatusOK, rendered)
}

type call struct {
	ctx       huma.Context
	response  http.ResponseWriter
	requestID string
	caller    principal.Principal
}

// begin admits a person in a session and nobody else: a program holding
// the account's key must never be able to send its mail.
func (r Routes) begin(ctx huma.Context) (call, bool) {
	c := call{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())}
	caller, authenticated := principal.FromContext(ctx.Context())
	if !authenticated {
		r.failed(c, "the route ran without an authenticated caller", nil)
		return c, false
	}
	if !caller.IsSession() {
		r.Errors.Write(c.response, http.StatusForbidden, "This action requires an interactive session", c.requestID)
		return c, false
	}
	c.caller = caller
	return c, true
}

func (r Routes) failed(c call, reason string, cause error) {
	attributes := []slog.Attr{slog.String("request_id", c.requestID), slog.String("reason", reason)}
	if cause != nil {
		attributes = append(attributes, slog.String("error", cause.Error()))
	}
	r.Logger.LogAttrs(c.ctx.Context(), slog.LevelError, "a mail draft route failed", attributes...)
	r.Errors.Write(c.response, http.StatusInternalServerError, errorenvelope.GenericServerMessage, c.requestID)
}

func (r Routes) answer(c call, view maildrafts.View) {
	rendered, err := json.Marshal(view)
	if err != nil {
		r.failed(c, "could not render the draft", err)
		return
	}
	// The go-ahead's token is in this body: nothing on the way may keep it.
	c.response.Header().Set("Cache-Control", "no-store")
	pythonjson.Send(c.response, http.StatusOK, rendered)
}

// statusOf is the HTTP status of each of the guard's refusals.
var statusOf = map[string]int{
	maildrafts.ErrNoDraft.Code:             http.StatusNotFound,
	maildrafts.ErrDraftChanged.Code:        http.StatusConflict,
	maildrafts.ErrConfirmationExpired.Code: http.StatusGone,
	maildrafts.ErrConfirmationSpent.Code:   http.StatusGone,
	maildrafts.ErrDraftNotPending.Code:     http.StatusGone,
	maildrafts.ErrConfirmationInvalid.Code: http.StatusForbidden,
	// The send is not wrong, it lacks the person's second confirmation:
	// 428, a precondition the request must meet, not a refusal of it.
	maildrafts.ErrUnusualNotConfirmed.Code: http.StatusPreconditionRequired,
	maildrafts.ErrUnusualChanged.Code:      http.StatusConflict,
}

// refused answers err if it is one of the guard's refusals or a send the
// server did not accept, and reports whether it did.
func (r Routes) refused(c call, err error) bool {
	var refusal *maildrafts.Refusal
	if errors.As(err, &refusal) {
		status, known := statusOf[refusal.Code]
		if !known {
			status = http.StatusBadRequest
		}
		r.Errors.WriteCoded(c.response, status, refusal.Code, refusal.Message, c.requestID)
		return true
	}
	var failure *maildrafts.SendFailure
	if errors.As(err, &failure) {
		r.Errors.WriteCoded(c.response, http.StatusBadGateway, "send_failed", failure.Message, c.requestID)
		return true
	}
	return false
}

func (r Routes) confirm(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok {
		return
	}
	view, err := r.Guard.Confirm(ctx.Context(), c.caller.UserID, ctx.Param("draft_id"))
	if r.refused(c, err) {
		return
	}
	if err != nil {
		r.failed(c, "could not show the draft", err)
		return
	}
	r.answer(c, view)
}

func (r Routes) send(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok {
		return
	}
	var body struct {
		Confirmation string `json:"confirmation"`
		ContentHash  string `json:"content_hash"`
		// Acknowledged is the codes of the unusual points the person
		// ticked on the card (maildrafts.Unusual); none for an everyday
		// message.
		Acknowledged []string `json:"acknowledged"`
	}
	if json.Unmarshal(requestbody.Buffered(ctx.Context()), &body) != nil || len(body.Confirmation) > 128 || len(body.ContentHash) > 128 ||
		!plausibleCodes(body.Acknowledged) {
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, "The body must be {\"confirmation\": ..., \"content_hash\": ..., \"acknowledged\": [...]}", c.requestID)
		return
	}
	view, err := r.Guard.Send(ctx.Context(), c.caller.UserID, ctx.Param("draft_id"), body.Confirmation, body.ContentHash, body.Acknowledged)
	if r.refused(c, err) {
		return
	}
	if err != nil {
		r.failed(c, "could not send the draft", err)
		return
	}
	r.answer(c, view)
}

// plausibleCodes bounds what a send may present as acknowledged: a few
// short words. There are five kinds of unusual point; anything far past
// that is not a card's request.
func plausibleCodes(codes []string) bool {
	if len(codes) > 16 {
		return false
	}
	for _, code := range codes {
		if len(code) > 64 {
			return false
		}
	}
	return true
}

func (r Routes) discard(ctx huma.Context) {
	c, ok := r.begin(ctx)
	if !ok {
		return
	}
	view, err := r.Guard.Discard(ctx.Context(), c.caller.UserID, ctx.Param("draft_id"))
	if r.refused(c, err) {
		return
	}
	if err != nil {
		r.failed(c, "could not discard the draft", err)
		return
	}
	r.answer(c, view)
}
