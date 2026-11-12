package nextcloudconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// LoginError is Nextcloud being unreachable or answering something this
// flow cannot use. Its text is safe to show: it holds a status or a
// sentence, never a URL, a token or a reply's body.
type LoginError struct{ Message string }

func (e *LoginError) Error() string { return e.Message }

func loginError(format string, args ...any) error {
	return &LoginError{Message: fmt.Sprintf(format, args...)}
}

// started is an open login flow: the page for the person, and the pair the
// server polls with.
type started struct {
	LoginURL     string
	PollEndpoint string
	PollToken    string
}

// granted is what Nextcloud hands over, once, when the person has agreed.
type granted struct {
	LoginName   string
	AppPassword string
}

// identity is who the new app password signs in as.
type identity struct {
	// UserID names the WebDAV files root. It differs from the login name
	// when people sign in with an email address.
	UserID      string
	DisplayName string
}

// requestTimeout bounds one request to Nextcloud. The person's browser is
// waiting on each.
const requestTimeout = 15 * time.Second

// replyLimit bounds a reply held in memory. The largest real one is a few
// hundred bytes.
const replyLimit = 1 << 20

// LoginAPI is Nextcloud's side of connecting: Login Flow v2, who the
// account is, and ending an app password. It speaks to one server only.
type LoginAPI struct {
	// base is the server, normalised: scheme, host and any sub-path.
	base string
	http *http.Client
	// agent is what Nextcloud lists the app password under in the
	// person's security settings.
	agent string
}

// NewLoginAPI builds the client for the deployment's server. transport is
// the process's; the client over it follows no redirect, because a
// redirect would carry the poll token or the app password wherever the
// reply said.
func NewLoginAPI(serverURL string, transport http.RoundTripper, agent string) (*LoginAPI, error) {
	switch {
	case serverURL == "":
		return nil, errors.New("nextcloudconnect: no Nextcloud server address")
	case transport == nil:
		return nil, errors.New("nextcloudconnect: no HTTP transport to reach Nextcloud with")
	case strings.TrimSpace(agent) == "":
		return nil, errors.New("nextcloudconnect: no name to give Nextcloud for the app password")
	}
	return &LoginAPI{
		base:  strings.TrimRight(serverURL, "/"),
		agent: agent,
		http: &http.Client{
			Transport:     transport,
			Timeout:       requestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}, nil
}

// sameOrigin reports whether address is on this API's server. The poll
// carries the token, so it may only go back where it came from.
func (a *LoginAPI) sameOrigin(address string) bool {
	there, err := url.Parse(address)
	here, hereErr := url.Parse(a.base)
	return err == nil && hereErr == nil && there.Scheme == here.Scheme && there.Host == here.Host
}

// send makes one request and returns the status and the (bounded) body.
func (a *LoginAPI) send(ctx context.Context, method, address string, form url.Values, login *granted) (int, []byte, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return 0, nil, loginError("Nextcloud named an address that cannot be used")
	}
	if form != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	request.Header.Set("User-Agent", a.agent)
	// Every OCS endpoint requires this header.
	request.Header.Set("OCS-APIRequest", "true")
	if login != nil {
		request.SetBasicAuth(login.LoginName, login.AppPassword)
	}
	response, err := a.http.Do(request)
	if err != nil {
		// The error's text holds the URL; only its kind is repeated.
		return 0, nil, loginError("Could not reach Nextcloud")
	}
	defer response.Body.Close()
	reply, err := io.ReadAll(io.LimitReader(response.Body, replyLimit))
	if err != nil {
		return 0, nil, loginError("Could not read Nextcloud's reply")
	}
	return response.StatusCode, reply, nil
}

// start opens a login flow.
func (a *LoginAPI) start(ctx context.Context) (started, error) {
	status, reply, err := a.send(ctx, http.MethodPost, a.base+"/index.php/login/v2", nil, nil)
	if err != nil {
		return started{}, err
	}
	if status != http.StatusOK {
		return started{}, loginError("Nextcloud answered %d to the login request", status)
	}
	var body struct {
		Login string `json:"login"`
		Poll  struct {
			Endpoint string `json:"endpoint"`
			Token    string `json:"token"`
		} `json:"poll"`
	}
	if json.Unmarshal(reply, &body) != nil || body.Poll.Token == "" {
		return started{}, loginError("Nextcloud sent an unexpected login reply")
	}
	// The page is opened by the person's browser: http(s) only, so a reply
	// cannot make the app hand them a javascript: link.
	page, err := url.Parse(body.Login)
	if err != nil || (page.Scheme != "http" && page.Scheme != "https") || page.Host == "" {
		return started{}, loginError("Nextcloud sent an unexpected login reply")
	}
	if !a.sameOrigin(body.Poll.Endpoint) {
		return started{}, loginError("Nextcloud named a different server to poll")
	}
	return started{LoginURL: body.Login, PollEndpoint: body.Poll.Endpoint, PollToken: body.Poll.Token}, nil
}

// poll asks once whether the person has granted access. A nil login with
// no error is "not yet": Nextcloud answers 404 until they have, and again
// after it has handed the password over.
func (a *LoginAPI) poll(ctx context.Context, endpoint, token string) (*granted, error) {
	if !a.sameOrigin(endpoint) {
		return nil, loginError("Refusing to poll another server")
	}
	status, reply, err := a.send(ctx, http.MethodPost, endpoint, url.Values{"token": {token}}, nil)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNotFound {
		return nil, nil
	}
	if status != http.StatusOK {
		return nil, loginError("Nextcloud answered %d while connecting", status)
	}
	var body struct {
		LoginName   string `json:"loginName"`
		AppPassword string `json:"appPassword"`
	}
	if json.Unmarshal(reply, &body) != nil {
		return nil, loginError("Nextcloud sent an unexpected login reply")
	}
	if body.LoginName == "" || strings.TrimSpace(body.AppPassword) == "" {
		return nil, loginError("Nextcloud sent an empty login")
	}
	return &granted{LoginName: body.LoginName, AppPassword: strings.TrimSpace(body.AppPassword)}, nil
}

// identity asks who the app password signs in as.
func (a *LoginAPI) identity(ctx context.Context, login granted) (identity, error) {
	status, reply, err := a.send(ctx, http.MethodGet, a.base+"/ocs/v2.php/cloud/user?format=json", nil, &login)
	if err != nil {
		return identity{}, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return identity{}, loginError("Nextcloud refused the new app password")
	}
	if status != http.StatusOK {
		return identity{}, loginError("Nextcloud answered %d for the account", status)
	}
	var body struct {
		OCS struct {
			Data struct {
				ID          string `json:"id"`
				DisplayName string `json:"display-name"`
				Displayname string `json:"displayname"`
			} `json:"data"`
		} `json:"ocs"`
	}
	if json.Unmarshal(reply, &body) != nil {
		return identity{}, loginError("Nextcloud sent an unexpected account reply")
	}
	data := body.OCS.Data
	if strings.TrimSpace(data.ID) == "" {
		return identity{}, loginError("Nextcloud sent no user id")
	}
	display := strings.TrimSpace(data.DisplayName)
	if display == "" {
		display = strings.TrimSpace(data.Displayname)
	}
	if display == "" {
		display = strings.TrimSpace(data.ID)
	}
	return identity{UserID: strings.TrimSpace(data.ID), DisplayName: display}, nil
}

// revoke deletes the app password at Nextcloud and reports whether it is
// gone. 401 is gone already. Anything else, a 403 for a real password that
// is not ours to delete included, is "not revoked".
func (a *LoginAPI) revoke(ctx context.Context, login granted) bool {
	status, _, err := a.send(ctx, http.MethodDelete, a.base+"/ocs/v2.php/core/apppassword", nil, &login)
	return err == nil && (status == http.StatusOK || status == http.StatusUnauthorized)
}

// listsFiles proves the app password can do what a connection is for: list
// the account's files. One PROPFIND of the files root, depth 0, which
// reads one entry and changes nothing.
func (a *LoginAPI) listsFiles(ctx context.Context, userID string, login granted) error {
	root := a.base + "/remote.php/dav/files/" + url.PathEscape(userID) + "/"
	request, err := http.NewRequestWithContext(ctx, "PROPFIND", root, nil)
	if err != nil {
		return loginError("Nextcloud sent a user id that cannot be used")
	}
	request.Header.Set("Depth", "0")
	request.Header.Set("User-Agent", a.agent)
	request.SetBasicAuth(login.LoginName, login.AppPassword)
	response, err := a.http.Do(request)
	if err != nil {
		return loginError("Could not reach Nextcloud")
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, replyLimit))
	if response.StatusCode != http.StatusMultiStatus {
		return loginError("Nextcloud answered %d when the account's files were listed", response.StatusCode)
	}
	return nil
}
