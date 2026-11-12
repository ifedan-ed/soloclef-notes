package threads

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
)

// Routes are the package's routes over one Service.
//
// The five that read a body (BodyRoutes) are buffered by the tier's body
// reader before the route runs, so cmd/api lists those pairs in
// `withBodies`.
type Routes struct {
	Service *Service
	Errors  errorenvelope.Writer
}

// route is one row of the table Register and BodyRoutes are written from.
type route struct {
	id, method, path, summary string
	// body says the route reads a request body.
	body  bool
	serve func(Routes, huma.Context)
}

// routes is every route of the package. The more specific path of two that
// overlap ("/threads/search" and "/threads/{thread_id}") needs no ordering:
// the tier's router prefers a literal segment to a wildcard.
var routes = []route{
	{"threads-create", http.MethodPost, ThreadsPath, "Start a thread", true, Routes.create},
	{"threads-list", http.MethodGet, ThreadsPath, "The caller's threads", false, Routes.list},
	{"threads-search", http.MethodGet, SearchPath, "The caller's threads whose title contains some text", false, Routes.search},
	{"threads-history", http.MethodGet, ThreadPath, "A thread's messages", false, Routes.history},
	{"threads-full", http.MethodGet, FullPath, "A thread with its messages", false, Routes.full},
	{"threads-update", http.MethodPut, ThreadPath, "Rename or archive a thread", true, Routes.update},
	{"threads-delete", http.MethodDelete, ThreadPath, "Delete a thread and everything saved for it", false, Routes.remove},
	{"threads-messages", http.MethodGet, MessagesPath, "A page of a thread's messages", false, Routes.messages},
	{"threads-append", http.MethodPost, MessagesPath, "Add a message to a thread", true, Routes.appendMessage},
	{"threads-share", http.MethodPost, LinksPath, "Make a public link to a thread", true, Routes.share},
	{"threads-links", http.MethodGet, LinksPath, "A thread's public links", false, Routes.links},
	{"threads-unshare", http.MethodDelete, LinkPath, "Take down one of a thread's public links", false, Routes.unshare},
	{"agent-tools", http.MethodGet, ToolsPath, "The tools the composer's tool picker lists", false, Routes.tools},
	{"notebook-threads", http.MethodGet, NotebookThreadsPath, "A notebook's chats", false, Routes.notebookThreads},
	{"notebook-threads-start", http.MethodPost, NotebookThreadsPath, "Start a chat in a notebook", false, Routes.startNotebookThread},
	{"notebook-threads-clear", http.MethodDelete, NotebookThreadsPath, "Delete a notebook's chats", false, Routes.clearNotebookThreads},
}

// Register mounts the routes.
func (r Routes) Register(api huma.API) {
	for _, entry := range routes {
		serve := entry.serve
		huma.Register(api, huma.Operation{
			OperationID: entry.id, Method: entry.method, Path: entry.path, Summary: entry.summary, Tags: []string{"chat"},
		}, func(context.Context, *struct{}) (*huma.StreamResponse, error) {
			return &huma.StreamResponse{Body: func(ctx huma.Context) { serve(r, ctx) }}, nil
		})
	}
}

// BodyRoutes is the "METHOD path" of every route that reads a body, for the
// tier's body reader.
func BodyRoutes() []string {
	var out []string
	for _, entry := range routes {
		if entry.body {
			out = append(out, entry.method+" "+entry.path)
		}
	}
	return out
}

// AllRoutes is the "METHOD path" of every route, for the tier's lists of
// what it serves.
func AllRoutes() []string {
	out := make([]string, len(routes))
	for i, entry := range routes {
		out[i] = entry.method + " " + entry.path
	}
	return out
}

// queryOf is the request's query string, parsed.
func queryOf(ctx huma.Context) url.Values {
	address := ctx.URL()
	return address.Query()
}

func (r Routes) answerer(ctx huma.Context, what string) answerer {
	return answerer{
		ctx: ctx, errors: r.Errors, logger: r.Service.Logger, response: humawriter.New(ctx),
		requestID: requestcontext.ID(ctx.Context()), what: what,
	}
}

// threadFor is the route's thread, if it is the caller's. It answers the
// request itself when it is not.
func (r Routes) threadFor(answer answerer) (principal.Principal, Thread, bool) {
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return caller, Thread{}, false
	}
	threadID, ok := answer.pathID("thread_id")
	if !ok {
		return caller, Thread{}, false
	}
	thread, err := r.Service.authorise(answer.ctx.Context(), caller, threadID)
	if err != nil {
		answer.refuse(err, "could not authorise the request")
		return caller, Thread{}, false
	}
	return caller, thread, true
}

func (r Routes) create(ctx huma.Context) {
	s := r.Service
	answer := r.answerer(ctx, "a thread could not be created")
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return
	}
	request, failures := readCreate(requestbody.Buffered(ctx.Context()))
	if len(failures) > 0 {
		answer.invalid(failures)
		return
	}
	// The thread is the caller's: the account comes from the request's
	// principal, and nothing in the body can name another.
	thread, err := s.Store.Create(ctx.Context(), caller.UserID, request.Title, request.Archived)
	if err != nil {
		answer.fail("could not write the thread", err)
		return
	}
	s.Events.Record(caller, "chat_created", func(properties *pythonjson.Object) {
		properties.Set("chat_id", thread.ID)
	})
	answer.send(http.StatusOK, recordOf(thread))
}

func (r Routes) list(ctx huma.Context) {
	s := r.Service
	answer := r.answerer(ctx, "an account's threads could not be listed")
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return
	}
	// limit bounds the active threads only; the archive is listed whole,
	// as the sidebar that reads both expects. No limit, zero and a
	// negative one all mean "all of them", which is what they have always
	// meant here.
	limit, absent, bad := queryNumber(queryOf(ctx), "limit")
	if bad != nil {
		answer.invalid([]errorenvelope.Failure{*bad})
		return
	}
	if absent || limit < 0 {
		limit = 0
	}
	active, err := s.Store.Listed(ctx.Context(), caller.UserID, false, limit)
	if err != nil {
		answer.fail("could not read the threads", err)
		return
	}
	archived, err := s.Store.Listed(ctx.Context(), caller.UserID, true, 0)
	if err != nil {
		answer.fail("could not read the archived threads", err)
		return
	}
	answer.send(http.StatusOK, threadList{Threads: listed(active), Archived: listed(archived)})
}

func (r Routes) search(ctx huma.Context) {
	answer := r.answerer(ctx, "an account's threads could not be searched")
	caller, signedIn := answer.signedIn()
	if !signedIn {
		return
	}
	query := queryOf(ctx)
	if !query.Has("title") {
		answer.invalid([]errorenvelope.Failure{{Location: []string{"query", "title"}, Message: "title is required"}})
		return
	}
	// U+0000 cannot be in a title (the column refuses it) and cannot be
	// sent to the database in a text parameter, so it matches nothing.
	text := query.Get("title")
	if strings.ContainsRune(text, 0) {
		answer.send(http.StatusOK, []listedThread{})
		return
	}
	found, err := r.Service.Store.Search(ctx.Context(), caller.UserID, text)
	if err != nil {
		answer.fail("could not search the threads", err)
		return
	}
	answer.send(http.StatusOK, listed(found))
}

func (r Routes) update(ctx huma.Context) {
	answer := r.answerer(ctx, "a thread could not be updated")
	caller, thread, ok := r.threadFor(answer)
	if !ok {
		return
	}
	request, failures := readUpdate(requestbody.Buffered(ctx.Context()))
	if len(failures) > 0 {
		answer.invalid(failures)
		return
	}
	updated, found, err := r.Service.Store.Update(ctx.Context(), caller.UserID, thread.ID, request.Title, request.Archived)
	if err != nil {
		answer.fail("could not write the thread", err)
		return
	}
	if !found {
		answer.refuse(errThreadAbsent, "")
		return
	}
	answer.send(http.StatusOK, recordOf(updated))
}

// remove deletes a thread: the engine's saved conversation first, then the
// rows, with the thread's lease held throughout (Service.hold).
//
// The order is so that a failure halfway leaves something whole. If the
// rows cannot be deleted after the saved conversation is gone, the thread
// is still there with its messages, and the engine starts its conversation
// again from them. The other order could leave a saved conversation with
// no thread, which nothing would ever remove: that is the leak the route
// this replaces had on every delete.
func (r Routes) remove(ctx huma.Context) {
	s := r.Service
	answer := r.answerer(ctx, "a thread could not be deleted")
	caller, thread, ok := r.threadFor(answer)
	if !ok {
		return
	}
	release, err := s.hold(ctx.Context(), thread.ID)
	if errors.Is(err, errStillStopping) {
		// The same refusal a new message gets while a turn is stopping,
		// with the same hint, so a client retries the same way.
		answer.response.Header().Set("Retry-After", "1")
		answer.refuse(&refusal{status: http.StatusConflict, code: codeTurnCancelling, message: stillStopping}, "")
		return
	}
	if err != nil {
		answer.fail("could not claim the thread", err)
		return
	}
	defer release()
	if err := s.Forget(ctx.Context(), thread.ID); err != nil {
		answer.fail("could not remove the thread's saved conversation", err)
		return
	}
	if err := s.Store.Delete(ctx.Context(), caller.UserID, thread.ID); err != nil {
		answer.fail("could not delete the thread", err)
		return
	}
	answer.send(http.StatusOK, said{Message: threadDeleted})
}

func (r Routes) history(ctx huma.Context) {
	answer := r.answerer(ctx, "a thread's messages could not be read")
	caller, thread, ok := r.threadFor(answer)
	if !ok {
		return
	}
	messages, err := r.Service.Store.Messages(ctx.Context(), caller.UserID, thread.ID, 0, 0)
	if err != nil {
		answer.fail("could not read the messages", err)
		return
	}
	answer.sendBody(http.StatusOK, func() ([]byte, error) { return historyBody(messages) })
}

func (r Routes) full(ctx huma.Context) {
	s := r.Service
	answer := r.answerer(ctx, "a thread could not be read")
	caller, thread, ok := r.threadFor(answer)
	if !ok {
		return
	}
	messages, err := s.Store.Messages(ctx.Context(), caller.UserID, thread.ID, 0, 0)
	if err != nil {
		answer.fail("could not read the messages", err)
		return
	}
	answer.sendBody(http.StatusOK, func() ([]byte, error) { return fullBody(thread, messages) })
}

func (r Routes) messages(ctx huma.Context) {
	answer := r.answerer(ctx, "a thread's messages could not be read")
	caller, thread, ok := r.threadFor(answer)
	if !ok {
		return
	}
	query := queryOf(ctx)
	var failures []errorenvelope.Failure
	skip, absent, bad := queryNumber(query, "skip")
	switch {
	case bad != nil:
		failures = append(failures, *bad)
	case absent:
		skip = 0
	case skip < 0:
		failures = append(failures, errorenvelope.Failure{Location: []string{"query", "skip"}, Message: "skip must be 0 or more"})
	}
	limit, absent, bad := queryNumber(query, "limit")
	switch {
	case bad != nil:
		failures = append(failures, *bad)
	case absent:
		limit = defaultMessagePage
	case limit < 1 || limit > maxMessagePage:
		failures = append(failures, errorenvelope.Failure{Location: []string{"query", "limit"}, Message: "limit must be between 1 and " + itoa(maxMessagePage)})
	}
	if len(failures) > 0 {
		answer.invalid(failures)
		return
	}
	messages, err := r.Service.Store.Messages(ctx.Context(), caller.UserID, thread.ID, skip, limit)
	if err != nil {
		answer.fail("could not read the messages", err)
		return
	}
	answer.sendBody(http.StatusOK, func() ([]byte, error) { return messagesBody(messages) })
}

// appendMessage adds a message a client wrote. The turn routes write a
// turn's own messages; this is what is left for a client: today, the note a
// page leaves when a request failed before any turn began.
func (r Routes) appendMessage(ctx huma.Context) {
	answer := r.answerer(ctx, "a message could not be added to a thread")
	caller, thread, ok := r.threadFor(answer)
	if !ok {
		return
	}
	request, failures := readAppend(requestbody.Buffered(ctx.Context()))
	if len(failures) > 0 {
		answer.invalid(failures)
		return
	}
	message, err := r.Service.Store.Append(ctx.Context(), caller.UserID, thread.ID, request.Role, request.Content, request.TurnID)
	if err != nil {
		answer.fail("could not write the message", err)
		return
	}
	answer.sendBody(http.StatusOK, func() ([]byte, error) {
		var b bytes.Buffer
		err := writeMessage(&b, message)
		return b.Bytes(), err
	})
}

func (r Routes) tools(ctx huma.Context) {
	answer := r.answerer(ctx, "the tool list could not be read")
	if _, signedIn := answer.signedIn(); !signedIn {
		return
	}
	answer.send(http.StatusOK, r.Service.Tools)
}
