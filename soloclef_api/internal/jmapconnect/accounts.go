package jmapconnect

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/errorenvelope"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
)

// shownAccount is a mailbox as every answer shows it: never a credential.
type shownAccount struct {
	ID             string  `json:"id"`
	Label          string  `json:"label"`
	ServerURL      string  `json:"server_url"`
	Username       string  `json:"username"`
	Auth           string  `json:"auth"`
	CanSend        bool    `json:"can_send"`
	NeedsReconnect bool    `json:"needs_reconnect"`
	CreatedAt      *string `json:"created_at"`
}

func shown(account jmapaccounts.Account) shownAccount {
	out := shownAccount{ID: account.ID, Label: account.Label, ServerURL: account.ServerURL, Username: account.Username,
		Auth: account.Auth, CanSend: account.CanSend, NeedsReconnect: account.NeedsReconnect}
	if !account.CreatedAt.IsZero() {
		created := account.CreatedAt.UTC().Format(time.RFC3339)
		out.CreatedAt = &created
	}
	return out
}

func (r Routes) list(ctx huma.Context) {
	c, ok := r.begin(ctx, false)
	if !ok {
		return
	}
	accounts, err := r.Mailboxes.Accounts(ctx.Context(), c.caller.UserID)
	if err != nil {
		r.failed(c, "could not read the mailboxes", err)
		return
	}
	out := make([]shownAccount, 0, len(accounts))
	for _, account := range accounts {
		out = append(out, shown(account))
	}
	r.send(c, http.StatusOK, out)
}

func bodyFailure(message string, path ...string) errorenvelope.Failure {
	return errorenvelope.Failure{Location: append([]string{requestbody.BodyRoot}, path...), Message: message}
}

// unstorable reports text no jsonb or text column may hold: U+0000.
func unstorable(texts ...string) bool {
	for _, text := range texts {
		if strings.ContainsRune(text, 0) {
			return true
		}
	}
	return false
}

// refusedByServer answers a connect or a test the server turned down: 400
// with the server's answer in this package's words.
func (r Routes) refusedByServer(c call, err error) bool {
	var failure *jmapmail.Failure
	if errors.As(err, &failure) {
		r.Errors.Write(c.response, http.StatusBadRequest, failure.Message, c.requestID)
		return true
	}
	var refused *jmapaccounts.Refused
	if errors.As(err, &refused) {
		r.Errors.Write(c.response, http.StatusBadRequest, refused.Message, c.requestID)
		return true
	}
	return false
}

func (r Routes) addAppPassword(ctx huma.Context) {
	c, ok := r.begin(ctx, true)
	if !ok {
		return
	}
	object, failure, err := requestbody.Decode(ctx.Header("Content-Type"), requestbody.Buffered(ctx.Context()))
	if err != nil {
		r.failed(c, "a body reached the route that was never buffered", err)
		return
	}
	if failure != nil {
		r.Errors.WriteValidation(c.response, c.requestID, []errorenvelope.Failure{*failure})
		return
	}
	model := requestbody.NewModel(object)
	label := strings.TrimSpace(model.TextOr("label", "", 0, 200))
	serverURL := strings.TrimSpace(model.Text("server_url", 1, 512))
	username := strings.TrimSpace(model.Text("username", 1, 320))
	password := strings.TrimSpace(model.Text("app_password", 1, 2000))
	failures := model.Failures()
	if len(failures) == 0 {
		for name, text := range map[string]string{"server_url": serverURL, "username": username, "app_password": password} {
			if text == "" {
				failures = append(failures, bodyFailure("This field must not be blank", name))
			}
		}
	}
	if len(failures) > 0 {
		r.Errors.WriteValidation(c.response, c.requestID, failures)
		return
	}
	if !strings.HasPrefix(strings.ToLower(serverURL), "https://") {
		r.Errors.Write(c.response, http.StatusBadRequest, addressInvalid, c.requestID)
		return
	}
	if unstorable(label, serverURL, username, password) {
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, "These settings contain a character that cannot be saved", c.requestID)
		return
	}
	account, err := r.Mailboxes.ConnectAppPassword(ctx.Context(), c.caller.UserID, label, serverURL, username, password)
	if r.refusedByServer(c, err) {
		return
	}
	if err != nil {
		r.failed(c, "could not connect the mailbox", err)
		return
	}
	r.send(c, http.StatusCreated, shown(account))
}

func (r Routes) remove(ctx huma.Context) {
	c, ok := r.begin(ctx, false)
	if !ok {
		return
	}
	revoked, err := r.Mailboxes.Disconnect(ctx.Context(), c.caller.UserID, ctx.Param("account_id"))
	if jmapaccounts.IsNoAccount(err) {
		r.Errors.Write(c.response, http.StatusNotFound, accountAbsent, c.requestID)
		return
	}
	if err != nil {
		r.failed(c, "could not remove the mailbox", err)
		return
	}
	// revoked is false for an app password (its owner deletes it in
	// Stalwart) and for a server that did not confirm: the website says
	// which to do.
	r.send(c, http.StatusOK, struct {
		Revoked bool `json:"revoked"`
	}{revoked})
}

// testResult is 200 either way: a mailbox that does not sign in is the
// test's answer, not a fault of the request.
type testResult struct {
	Status       string  `json:"status"`
	MailboxCount *int    `json:"mailbox_count"`
	CanSend      *bool   `json:"can_send"`
	Message      *string `json:"message"`
}

func (r Routes) test(ctx huma.Context) {
	c, ok := r.begin(ctx, true)
	if !ok {
		return
	}
	probe, err := r.Mailboxes.Test(ctx.Context(), c.caller.UserID, ctx.Param("account_id"))
	var failure *jmapmail.Failure
	switch {
	case jmapaccounts.IsNoAccount(err):
		r.Errors.Write(c.response, http.StatusNotFound, accountAbsent, c.requestID)
	case errors.As(err, &failure):
		r.send(c, http.StatusOK, testResult{Status: "error", Message: &failure.Message})
	case err != nil:
		r.failed(c, "the mailbox test failed", err)
	default:
		r.send(c, http.StatusOK, testResult{Status: "ok", MailboxCount: &probe.Mailboxes, CanSend: &probe.CanSend})
	}
}
