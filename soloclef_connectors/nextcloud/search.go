package nextcloud

import (
	"context"
	"encoding/xml"
	"net/http"
	"strconv"
	"strings"
)

// searchBody is a WebDAV SEARCH (RFC 5323) for names that contain a text,
// anywhere under the user's files: the request Nextcloud's own file search
// is built on. scope and pattern are already XML-escaped.
func searchBody(scope, pattern string, limit int) string {
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<d:searchrequest xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns" xmlns:nc="http://nextcloud.org/ns">` +
		`<d:basicsearch><d:select><d:prop>` + requestedProps + `</d:prop></d:select>` +
		`<d:from><d:scope><d:href>` + scope + `</d:href><d:depth>infinity</d:depth></d:scope></d:from>` +
		`<d:where><d:like><d:prop><d:displayname/></d:prop><d:literal>` + pattern + `</d:literal></d:like></d:where>` +
		`<d:orderby/><d:limit><d:nresults>` + strconv.Itoa(limit) + `</d:nresults></d:limit>` +
		`</d:basicsearch></d:searchrequest>`
}

// xmlText is text made safe to stand inside an XML element.
func xmlText(text string) string {
	var escaped strings.Builder
	// The only error is the writer's, and a Builder has none.
	_ = xml.EscapeText(&escaped, []byte(text))
	return escaped.String()
}

// searchTerm is the text as it can be searched for: "%" and "_" are the
// pattern's own wildcards, and are dropped, since a person who types one
// means the character and the server would read it as "anything".
func searchTerm(text string) string {
	return strings.NewReplacer("%", "", "_", "").Replace(strings.TrimSpace(text))
}

// Search is the files and folders anywhere in the account whose name
// contains the text: folders first, then by name without regard to case.
// A text that is nothing but wildcards finds nothing, and asks nothing.
func (c *Client) Search(ctx context.Context, text string, limit int) ([]Entry, error) {
	term := searchTerm(text)
	if term == "" || limit < 1 {
		return []Entry{}, nil
	}
	ctx, done := context.WithTimeout(ctx, c.timing.Listing)
	defer done()
	body := searchBody(xmlText("/files/"+c.account.Username), xmlText("%"+term+"%"), limit)
	response, err := c.sendTo(ctx, "SEARCH", strings.TrimRight(c.account.BaseURL, "/")+"/remote.php/dav/", "the search",
		http.Header{"Content-Type": {"text/xml"}}, body)
	if err != nil {
		return nil, err
	}
	found, err := c.listing(response, "the search")
	if err != nil {
		return nil, err
	}
	// Without the root (a search can return the scope itself), and in the
	// order a folder is listed in, which is the same every time: the
	// server's own is not promised.
	kept := children(found, Root)
	if len(kept) > limit {
		kept = kept[:limit]
	}
	return kept, nil
}
