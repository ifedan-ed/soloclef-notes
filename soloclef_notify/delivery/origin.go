package delivery

import (
	"fmt"
	"net/url"
	"strings"
)

// ServerOrigin is the address phones are set to, in the one form the phone
// app compares (its deepLinks.ts originOf): "scheme://host[:port]", the host
// in lower case with no trailing dot, the scheme's own port left out, no
// path. A push carries it as data.server, and the app ignores a push from a
// server other than the one it is set to now (a server chosen before that
// kept this phone's token).
//
// An international host must be given in its ASCII (xn--) form: turning it
// into that form here would need the IDNA tables, and getting it subtly
// different from the app's would make every push be ignored. An address
// that is not plain http(s) with a host is refused, not guessed at.
func ServerOrigin(address string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil {
		return "", fmt.Errorf("delivery: the public address %q: %w", address, err)
	}
	scheme := strings.ToLower(parsed.Scheme)
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	switch {
	case scheme != "https" && scheme != "http":
		return "", fmt.Errorf("delivery: the public address %q is not http or https", address)
	case host == "" || strings.Contains(host, ":"):
		return "", fmt.Errorf("delivery: the public address %q has no host the app could be set to", address)
	}
	for _, r := range host {
		if r > 0x7e || r <= 0x20 {
			return "", fmt.Errorf("delivery: give the public address %q with its host in ASCII (xn--) form", address)
		}
	}
	port := parsed.Port()
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		port = ""
	}
	if port != "" {
		return scheme + "://" + host + ":" + port, nil
	}
	return scheme + "://" + host, nil
}
