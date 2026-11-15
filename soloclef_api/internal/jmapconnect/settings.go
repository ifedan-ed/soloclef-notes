package jmapconnect

import (
	"errors"
	"net/url"
	"strings"
)

// Settings is what the deployment says about Stalwart's sign-in. ClientID
// and RedirectURI are set together or not at all: with neither, mailboxes
// connect by app password only and the availability route says so.
type Settings struct {
	// ClientID is the https address of this app's client metadata
	// document, served at ClientDocumentPath (JMAP_MAIL_CLIENT_ID).
	ClientID string
	// RedirectURI is the callback's address (JMAP_MAIL_REDIRECT_URI).
	RedirectURI string
	// Name is the app's name on the server's consent page.
	Name string
	// FrontendURL is the website, where the callback sends the person.
	FrontendURL string
	// SecretKey seals the attempt's cookie.
	SecretKey string
}

// Configured reports whether Stalwart's sign-in is offered.
func (s Settings) Configured() bool { return s.ClientID != "" && s.RedirectURI != "" }

// pathOf is an address's path, or "" for one that is not an https URL
// without a query or a fragment.
func pathOf(address string) string {
	parsed, err := url.Parse(address)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
		return ""
	}
	return parsed.Path
}

// Check reports a deployment set up halfway or wrongly. Called once at
// start-up: a mistake here is found then, not at a person's first sign-in.
func (s Settings) Check() error {
	switch {
	case s.SecretKey == "":
		return errors.New("jmapconnect: the secret key the attempt's cookie is sealed with is not set")
	case (s.ClientID == "") != (s.RedirectURI == ""):
		return errors.New("jmapconnect: JMAP_MAIL_CLIENT_ID and JMAP_MAIL_REDIRECT_URI must be set together or not at all")
	case !s.Configured():
		return nil
	case pathOf(s.ClientID) != ClientDocumentPath:
		return errors.New("jmapconnect: JMAP_MAIL_CLIENT_ID must be the https address of " + ClientDocumentPath + " on this site, with no query")
	case pathOf(s.RedirectURI) != CallbackPath:
		return errors.New("jmapconnect: JMAP_MAIL_REDIRECT_URI must be the https address of " + CallbackPath + " on this site")
	case strings.TrimSpace(s.FrontendURL) == "":
		return errors.New("jmapconnect: Stalwart sign-in is set up and FRONTEND_URL is not: the callback has nowhere to send the person")
	case strings.TrimSpace(s.Name) == "":
		return errors.New("jmapconnect: the app's name for the consent page is not set")
	}
	return nil
}
