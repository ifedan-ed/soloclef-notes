package jmapconnect

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/humawriter"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestcontext"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapaccounts"
	"git.danvics.com/danvics/soloclef/soloclef_assistant/jmapmail"
)

// availability is open to the account's API keys too: it says one thing
// about the deployment and nothing about the caller.
func (r Routes) availability(ctx huma.Context) {
	c, ok := r.begin(ctx, false)
	if !ok {
		return
	}
	r.send(c, http.StatusOK, struct {
		Configured bool `json:"configured"`
	}{r.Settings.Configured()})
}

// connect checks the server the person named offers Stalwart's sign-in
// and accepts this app, seals the attempt into this browser's cookie and
// answers the server's sign-in page and the server's own address.
func (r Routes) connect(ctx huma.Context) {
	c, ok := r.begin(ctx, true)
	if !ok {
		return
	}
	if !r.Settings.Configured() {
		r.Errors.WriteCoded(c.response, http.StatusServiceUnavailable, notConfiguredCode, notConfigured, c.requestID)
		return
	}
	var body struct {
		ServerURL  string `json:"server_url"`
		ReturnPath string `json:"return_path"`
		LoginHint  string `json:"login_hint"`
	}
	if json.Unmarshal(requestbody.Buffered(ctx.Context()), &body) != nil {
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, "The body must be a JSON object", c.requestID)
		return
	}
	if len(body.ServerURL) > 512 || len(body.ReturnPath) > 512 || len(body.LoginHint) > 320 {
		r.Errors.Write(c.response, http.StatusUnprocessableEntity, "server_url and return_path are at most 512 characters, login_hint 320", c.requestID)
		return
	}
	origin, err := jmapmail.Origin(body.ServerURL)
	if err != nil || !strings.HasPrefix(origin, "https://") {
		r.Errors.Write(c.response, http.StatusBadRequest, addressInvalid, c.requestID)
		return
	}
	metadata, err := r.OAuth.Discover(ctx.Context(), origin)
	if r.refusedByServer(c, err) {
		return
	}
	if err != nil {
		r.failed(c, "could not read the server's sign-in metadata", err)
		return
	}
	// From here the server is the one the metadata confirmed, which may
	// not be the address typed (jmapaccounts.OAuth.Discover): the attempt,
	// the preflight, the token exchange and the stored mailbox all use it,
	// and nothing more is sent to the typed address.
	server := metadata.Server
	now := r.Now()
	begun, err := newAttempt(c.caller.UserID, server, metadata, body.ReturnPath, now)
	if err != nil {
		r.failed(c, "could not start the sign-in", err)
		return
	}
	address := jmapaccounts.AuthorizationRequest(metadata, r.Settings.ClientID, r.Settings.RedirectURI, begun.State,
		begun.challenge(), strings.TrimSpace(body.LoginHint))
	// Asked before the person is sent: a server that will not accept this
	// app shows its own error page with no way back here.
	if err := r.OAuth.Preflight(ctx.Context(), server, address); r.refusedByServer(c, err) {
		return
	} else if err != nil {
		r.failed(c, "could not ask the server about this app", err)
		return
	}
	sealed, err := begun.seal(r.Settings.SecretKey, now)
	if err != nil {
		r.failed(c, "could not seal the sign-in attempt", err)
		return
	}
	r.setAttemptCookie(c.response, sealed)
	// server_url is the address the mailbox will be connected with, so a
	// caller can say which server it is.
	r.send(c, http.StatusOK, struct {
		AuthURL   string `json:"auth_url"`
		ServerURL string `json:"server_url"`
	}{address, server})
}

func (r Routes) cookie(value string, maxAge int) *http.Cookie {
	path := CallbackPath
	if parsed, err := url.Parse(r.Settings.RedirectURI); err == nil && parsed.Path != "" {
		path = parsed.Path
	}
	return &http.Cookie{Name: cookieName, Value: value, Path: path, MaxAge: maxAge, HttpOnly: true,
		Secure: strings.HasPrefix(r.Settings.RedirectURI, "https://"),
		// Lax: sent on the server's top-level redirect back to this site.
		SameSite: http.SameSiteLaxMode}
}

func (r Routes) setAttemptCookie(response http.ResponseWriter, sealed string) {
	http.SetCookie(response, r.cookie(sealed, int(attemptLifetime/time.Second)))
}

// backToSite ends the callback: the cookie cleared, the browser sent to the
// page the sign-in started on with `?jmap_mail=<outcome>`.
func (r Routes) backToSite(c call, returnPath, outcome string) {
	page := returnPath
	if page == "" {
		page = connectorsPage
	}
	separator := "?"
	if strings.Contains(page, "?") {
		separator = "&"
	}
	http.SetCookie(c.response, r.cookie("", -1))
	header := c.response.Header()
	header.Set("Location", strings.TrimRight(r.Settings.FrontendURL, "/")+page+separator+url.Values{outcomeParam: {outcome}}.Encode())
	// The address held the server's code; it is not to be kept or passed on.
	header.Set("Cache-Control", "no-store")
	header.Set("Referrer-Policy", "no-referrer")
	c.response.WriteHeader(http.StatusSeeOther)
}

// callback is where the server sends the person back. It has no session:
// the attempt's cookie says who is connecting and which server, and only
// the browser that started the sign-in has it.
func (r Routes) callback(ctx huma.Context) {
	c := call{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())}
	if !r.Settings.Configured() {
		r.Errors.WriteCoded(c.response, http.StatusServiceUnavailable, notConfiguredCode, notConfigured, c.requestID)
		return
	}
	sealed := ""
	if cookie, err := huma.ReadCookie(ctx, cookieName); err == nil {
		sealed = cookie.Value
	}
	begun, err := reopen(sealed, ctx.Query("state"), ctx.Query("iss"), r.Settings.SecretKey, r.Now())
	if err != nil {
		r.backToSite(c, "", outcomeExpired)
		return
	}
	refuse := func(outcome, reason string) {
		r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "a JMAP mailbox sign-in was refused",
			slog.String("request_id", c.requestID), slog.String("outcome", outcome), slog.String("reason", reason))
		r.backToSite(c, begun.ReturnPath, outcome)
	}
	if said := ctx.Query("error"); said != "" {
		if said == "access_denied" {
			refuse(outcomeDenied, "the person declined")
		} else {
			refuse(outcomeFailed, "the server's page returned an error")
		}
		return
	}
	code := ctx.Query("code")
	if code == "" {
		refuse(outcomeFailed, "the callback carries no code")
		return
	}
	// The code is spent by the exchange: from here the grant is saved or
	// lost, so the rest runs to its end even if the browser has gone.
	settle := context.WithoutCancel(ctx.Context())
	grant, err := r.OAuth.Exchange(settle, begun.TokenEndpoint, r.Settings.ClientID, r.Settings.RedirectURI, code, begun.Verifier, r.Now())
	if err != nil {
		refuse(outcomeFailed, err.Error())
		return
	}
	if !grant.HasMail() {
		refuse(outcomeNoMail, "the grant has no mail scope")
		return
	}
	if _, err := r.Mailboxes.ConnectGrant(settle, begun.UserID, begun.ServerURL, r.Settings.ClientID, begun.metadata(), grant); err != nil {
		var failure *jmapmail.Failure
		if errors.As(err, &failure) {
			refuse(outcomeNoMail, failure.Message)
			return
		}
		r.Logger.LogAttrs(ctx.Context(), slog.LevelError, "a granted JMAP mailbox could not be saved",
			slog.String("request_id", c.requestID), slog.String("error", err.Error()))
		r.backToSite(c, begun.ReturnPath, outcomeFailed)
		return
	}
	r.Logger.LogAttrs(ctx.Context(), slog.LevelInfo, "JMAP mailbox connected", slog.String("user_id", begun.UserID))
	r.backToSite(c, begun.ReturnPath, outcomeConnected)
}

// clientDocument is this app's client metadata document: the client id is
// this document's own address, and the server reads the one redirect URI
// it may send a code to from here. Public, and cached by the server for at
// least five minutes; a day is the most it keeps one.
func (r Routes) clientDocument(ctx huma.Context) {
	c := call{ctx: ctx, response: humawriter.New(ctx), requestID: requestcontext.ID(ctx.Context())}
	if !r.Settings.Configured() {
		r.Errors.WriteCoded(c.response, http.StatusNotFound, notConfiguredCode, notConfigured, c.requestID)
		return
	}
	site := strings.TrimRight(r.Settings.FrontendURL, "/") + "/"
	document := map[string]any{
		"client_id":                  r.Settings.ClientID,
		"client_name":                r.Settings.Name,
		"client_uri":                 site,
		"redirect_uris":              []string{r.Settings.RedirectURI},
		"token_endpoint_auth_method": "none",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
		"scope":                      jmapaccounts.MailScope,
	}
	c.response.Header().Set("Cache-Control", "public, max-age=3600")
	r.send(c, http.StatusOK, document)
}
