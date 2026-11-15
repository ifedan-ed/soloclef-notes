package jmapaccounts

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/mcptools"
)

// privateEndings are host name endings that only mean something inside a
// network: a mail server named so is refused before any lookup.
var privateEndings = []string{".local", ".localhost", ".internal", ".lan", ".home.arpa"}

// PublicNetwork is the production jmapmail.Network: the one place that
// decides where a JMAP mailbox's requests may go. It is mcptools'
// PublicInternet, the guard the connected apps' servers already pass
// through: https on port 443 only, a named host and no other (the server's
// own), resolved and checked to be public at every connection and then
// connected to by the checked address, no redirects, no proxy. A host
// without a dot, or with a private ending, is refused here first.
type PublicNetwork struct {
	Internet mcptools.PublicInternet
}

// Check reports what is missing.
func (p PublicNetwork) Check() error { return p.Internet.Check() }

func (p PublicNetwork) Client(serverURL string) (*http.Client, error) {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return nil, errors.New("jmapaccounts: the server's address is not a URL")
	}
	host := strings.ToLower(strings.TrimRight(parsed.Hostname(), "."))
	if !strings.Contains(host, ".") {
		return nil, errors.New("jmapaccounts: the server's host is not a public name")
	}
	for _, ending := range privateEndings {
		if strings.HasSuffix(host, ending) {
			return nil, errors.New("jmapaccounts: the server's host is not a public name")
		}
	}
	return p.Internet.Client(serverURL, []string{host})
}
