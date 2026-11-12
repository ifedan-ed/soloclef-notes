package notebookresearch

import (
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"net/netip"
	"net/url"
	"sort"
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_assistant/pagereader"
)

// maxURLChars is the longest address taken; browsers and servers agree on
// about this much.
const maxURLChars = 2048

// trackers are query parameters that record a click and never change the
// page, so two addresses differing only in them are one source.
var trackers = map[string]bool{
	"fbclid": true, "gclid": true, "dclid": true, "msclkid": true,
	"mc_cid": true, "mc_eid": true, "ref": true, "ref_src": true,
}

// webAddress is the trimmed address when it is an http(s) one with a host,
// which is the only kind that can be read, cited and opened.
func webAddress(raw string) (*url.URL, string, bool) {
	text := strings.TrimSpace(raw)
	if text == "" || len(text) > maxURLChars {
		return nil, "", false
	}
	parsed, err := url.Parse(text)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" {
		return nil, "", false
	}
	return parsed, text, true
}

// plainlyPrivate reports an address that names this machine or a private
// network in so many words: "localhost", or a literal IP address the page
// reader would refuse. It lets a pasted address be turned down at once,
// with a sentence. It is a courtesy and not the guard: a name that resolves
// to a private address is refused by the reader when it connects, on every
// redirect, which is the only place that check can be made honestly.
func plainlyPrivate(address *url.URL) bool {
	host := strings.ToLower(address.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return !pagereader.PublicAddress(ip.Unmap())
	}
	return false
}

// sameSourceKey is what two addresses of one page share: the scheme, the
// host's case, a leading "www.", a trailing slash, the order of the query
// and its trackers are all left out of it.
func sameSourceKey(address *url.URL) string {
	host := strings.TrimPrefix(strings.ToLower(address.Hostname()), "www.")
	if port := address.Port(); port != "" && port != "80" && port != "443" {
		host += ":" + port
	}
	type pair struct{ key, value string }
	var kept []pair
	for key, values := range address.Query() {
		lower := strings.ToLower(key)
		if trackers[lower] || strings.HasPrefix(lower, "utm_") {
			continue
		}
		for _, value := range values {
			kept = append(kept, pair{key, value})
		}
	}
	sort.Slice(kept, func(i, j int) bool {
		if kept[i].key != kept[j].key {
			return kept[i].key < kept[j].key
		}
		return kept[i].value < kept[j].value
	})
	key := host + strings.TrimRight(address.EscapedPath(), "/")
	for i, p := range kept {
		separator := "&"
		if i == 0 {
			separator = "?"
		}
		key += separator + url.QueryEscape(p.key) + "=" + url.QueryEscape(p.value)
	}
	return key
}

// resultID is a result row's id: equal for two addresses of one page, so a
// page two sources both found is listed once.
func resultID(address *url.URL) string {
	sum := sha1.Sum([]byte(sameSourceKey(address)))
	return hex.EncodeToString(sum[:])
}

// importIdentity is the identity an imported page carries in
// documents.unique_identifier_hash, which is unique per account: importing
// the same page twice, even at once, makes one document.
func importIdentity(address *url.URL) string {
	sum := sha256.Sum256([]byte("research:" + sameSourceKey(address)))
	return hex.EncodeToString(sum[:])
}
