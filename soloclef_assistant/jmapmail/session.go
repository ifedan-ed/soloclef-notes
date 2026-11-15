package jmapmail

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Session is what the server's session resource says about one sign-in:
// where to send requests, which account holds the mail, and whether it may
// send.
type Session struct {
	// Origin is the server, as Origin gives it: every URL below is on it.
	Origin string
	// APIURL is where method calls are posted.
	APIURL string
	// DownloadURL is the template a blob is downloaded from (RFC 8620
	// 2: {accountId}, {blobId}, {name}, {type}).
	DownloadURL string
	// UploadURL is the template a file is uploaded to (RFC 8620 6.1:
	// {accountId}), "" when the server offers no upload.
	UploadURL string
	// Username is the name the server knows the sign-in by; for Stalwart
	// the account's address.
	Username string
	// AccountID is the primary account for mail.
	AccountID string
	// CanSend says the session offers JMAP submission for that account.
	// Stalwart 1.0 offers it to an OAuth token with the mail scope and to
	// an app password, even one whose emailSend permission is disabled:
	// that permission governs SMTP submission only (found against a real
	// server). What stands between a draft and a send here is the guard
	// (soloclef_assistant/maildrafts), not the credential.
	CanSend bool
	// MaxObjectsInGet bounds how many ids one /get may name.
	MaxObjectsInGet int
	// MaxCallsInRequest bounds how many method calls one request may
	// carry.
	MaxCallsInRequest int
	// MaxSizeUpload is the largest file the server accepts in one upload
	// (RFC 8620 2, the core capability), in bytes: the largest file a
	// message from this mailbox can carry. 0 when the server did not say.
	MaxSizeUpload int64
	// MaxSizeAttachmentsPerEmail is the most, in bytes, all of one
	// message's attachments together may be (RFC 8621 1.3.1, the mail
	// account capability). 0 when the server did not say.
	MaxSizeAttachmentsPerEmail int64
}

// sessionResource is the part of RFC 8620's session object that is read.
type sessionResource struct {
	Capabilities    map[string]json.RawMessage `json:"capabilities"`
	Accounts        map[string]sessionAccount  `json:"accounts"`
	PrimaryAccounts map[string]string          `json:"primaryAccounts"`
	Username        string                     `json:"username"`
	APIURL          string                     `json:"apiUrl"`
	DownloadURL     string                     `json:"downloadUrl"`
	UploadURL       string                     `json:"uploadUrl"`
}

type sessionAccount struct {
	Name                string                     `json:"name"`
	IsReadOnly          bool                       `json:"isReadOnly"`
	AccountCapabilities map[string]json.RawMessage `json:"accountCapabilities"`
}

type coreCapability struct {
	MaxObjectsInGet   int   `json:"maxObjectsInGet"`
	MaxCallsInRequest int   `json:"maxCallsInRequest"`
	MaxSizeUpload     int64 `json:"maxSizeUpload"`
}

type mailAccountCapability struct {
	MaxSizeAttachmentsPerEmail int64 `json:"maxSizeAttachmentsPerEmail"`
}

// wellKnownPath is where RFC 8620 2.2 says a session is found.
const wellKnownPath = "/.well-known/jmap"

// Session finds the session of a sign-in on the server at serverURL. A
// *Failure is a server that is not JMAP mail, refused the credential or
// could not be reached, worded for the person.
func (c Client) Session(ctx context.Context, serverURL string, credential Credential) (Session, error) {
	ctx, cancel := c.bounded(ctx)
	defer cancel()
	return c.session(ctx, serverURL, credential)
}

// session is Session within a deadline the caller already set, so that a
// method that finds the session and then calls the API has one deadline
// for both.
func (c Client) session(ctx context.Context, serverURL string, credential Credential) (Session, error) {
	origin, err := Origin(serverURL)
	if err != nil {
		return Session{}, errNotJMAP
	}
	address := origin + wellKnownPath
	var raw []byte
	for hop := 0; ; hop++ {
		status, header, body, err := c.exchange(ctx, origin, credential, http.MethodGet, address, nil, mostSessionBytes)
		if err != nil {
			return Session{}, err
		}
		if status >= 300 && status < 400 && header.Get("Location") != "" {
			// Followed by hand and only on the same origin: the client
			// itself follows none (see the package comment).
			if hop == mostRedirects {
				return Session{}, errNotJMAP
			}
			if address, err = onOrigin(origin, address, header.Get("Location")); err != nil {
				return Session{}, err
			}
			continue
		}
		if status != http.StatusOK {
			return Session{}, errNotJMAP
		}
		raw = body
		break
	}
	var resource sessionResource
	if json.Unmarshal(raw, &resource) != nil || resource.Capabilities == nil {
		return Session{}, errNotJMAP
	}
	if _, core := resource.Capabilities[capabilityCore]; !core {
		return Session{}, errNotJMAP
	}
	if _, mail := resource.Capabilities[capabilityMail]; !mail {
		return Session{}, errNoMail
	}
	accountID := resource.PrimaryAccounts[capabilityMail]
	account, known := resource.Accounts[accountID]
	if accountID == "" || !known {
		return Session{}, errNoMail
	}
	session := Session{Origin: origin, Username: strings.TrimSpace(resource.Username), AccountID: accountID}
	if resource.APIURL == "" {
		return Session{}, errNotJMAP
	}
	if session.APIURL, err = onOrigin(origin, address, resource.APIURL); err != nil {
		// The usual cause is a person who typed another name for the
		// server (an alias, its IP, an internal name) than the public
		// address it builds its URLs from: say which one it gave, and hand
		// it to the caller, which decides whether it may be used.
		if named, parseErr := Origin(resource.APIURL); parseErr == nil {
			return Session{}, &Failure{Message: "This server calls itself " + plainText(named, 120) + "; connect with that address", Elsewhere: named}
		}
		return Session{}, err
	}
	if resource.DownloadURL != "" {
		if session.DownloadURL, err = templateOnOrigin(origin, resource.DownloadURL); err != nil {
			return Session{}, err
		}
	}
	if resource.UploadURL != "" {
		if session.UploadURL, err = templateOnOrigin(origin, resource.UploadURL); err != nil {
			return Session{}, err
		}
	}
	_, offered := resource.Capabilities[capabilitySubmission]
	_, granted := account.AccountCapabilities[capabilitySubmission]
	session.CanSend = offered && granted && !account.IsReadOnly
	var core coreCapability
	_ = json.Unmarshal(resource.Capabilities[capabilityCore], &core)
	// The field is required by RFC 8620; a server that leaves it out is
	// asked for no more than a thread's worth at once, which every server
	// allows, rather than refused for a missing number.
	session.MaxObjectsInGet = core.MaxObjectsInGet
	if session.MaxObjectsInGet <= 0 {
		session.MaxObjectsInGet = 50
	}
	// Required by RFC 8620 as well; RFC 8620 asks every server to allow at
	// least 16 calls, which Stalwart gives, so a server silent on it is
	// sent no more than that.
	session.MaxCallsInRequest = core.MaxCallsInRequest
	if session.MaxCallsInRequest <= 0 {
		session.MaxCallsInRequest = 16
	}
	// The two size limits are the server's to state and are passed on as
	// stated: 0 is "not said", and a caller checks a file against a limit
	// only when there is one, leaving the server to refuse what it will.
	session.MaxSizeUpload = max(core.MaxSizeUpload, 0)
	var mail mailAccountCapability
	_ = json.Unmarshal(account.AccountCapabilities[capabilityMail], &mail)
	session.MaxSizeAttachmentsPerEmail = max(mail.MaxSizeAttachmentsPerEmail, 0)
	return session, nil
}

// templateOnOrigin is a URL template of the session (the download one) as
// an absolute template on origin. It is not resolved as a URL: that would
// percent-encode the template's braces, and the placeholders would no
// longer be found. A template that starts with one slash is a path on the
// origin (Stalwart's are); an absolute one must name the origin itself.
func templateOnOrigin(origin, template string) (string, error) {
	if strings.HasPrefix(template, "/") && !strings.HasPrefix(template, "//") {
		return origin + template, nil
	}
	if got, err := Origin(template); err != nil || got != origin {
		return "", &Failure{Message: "The mail server pointed to an address on another server, which is not followed"}
	}
	return template, nil
}
