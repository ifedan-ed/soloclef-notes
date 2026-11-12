package nextcloud

import (
	"encoding/xml"
	"fmt"
	"net/mail"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Entry is one file or folder as Nextcloud describes it.
type Entry struct {
	Path     string
	Name     string
	IsFolder bool
	// Size is the file's length in bytes, and nil when the server did not
	// say: a missing size is not zero, and is not treated as one.
	Size *int64
	// Modified is nil when the server sent no date or one that cannot be
	// read.
	Modified    *time.Time
	ContentType string
	// ETag changes when the file's bytes do; it is how a file imported
	// before is known to be unchanged. Empty when the server sent none.
	ETag string
	// FileID is Nextcloud's own number for the file, which stays the same
	// when the file is moved or renamed; FileURL makes the address a
	// person opens it at. Empty when the server sent none.
	FileID string
}

// FileURL is the address at which a person opens a file in Nextcloud's
// web interface: {server}/f/{fileid}, which Nextcloud redirects to the
// file wherever it now is. "" when the file's id is not known, since a
// path-based link would break the moment the file is moved.
func FileURL(serverURL, fileID string) string {
	if fileID == "" {
		return ""
	}
	return strings.TrimRight(serverURL, "/") + "/f/" + url.PathEscape(fileID)
}

// requestedProps is the properties asked for on every listing.
const requestedProps = "<d:displayname/><d:resourcetype/><d:getcontentlength/>" +
	"<d:getlastmodified/><d:getcontenttype/><d:getetag/><oc:size/><oc:fileid/>"

// propfindBody is the PROPFIND request: only what an Entry holds.
const propfindBody = `<?xml version="1.0" encoding="UTF-8"?>` +
	`<d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns" xmlns:nc="http://nextcloud.org/ns">` +
	`<d:prop>` + requestedProps + `</d:prop></d:propfind>`

// multistatus is a WebDAV reply. encoding/xml matches elements by namespace
// and local name, so the prefixes a server chooses do not matter.
type multistatus struct {
	XMLName   xml.Name      `xml:"DAV: multistatus"`
	Responses []davResponse `xml:"DAV: response"`
}

type davResponse struct {
	Href      string        `xml:"DAV: href"`
	Propstats []davPropstat `xml:"DAV: propstat"`
}

type davPropstat struct {
	Status string  `xml:"DAV: status"`
	Prop   davProp `xml:"DAV: prop"`
}

type davProp struct {
	DisplayName   *string          `xml:"DAV: displayname"`
	ResourceType  *davResourceType `xml:"DAV: resourcetype"`
	ContentLength *string          `xml:"DAV: getcontentlength"`
	LastModified  *string          `xml:"DAV: getlastmodified"`
	ContentType   *string          `xml:"DAV: getcontenttype"`
	ETag          *string          `xml:"DAV: getetag"`
	Size          *string          `xml:"http://owncloud.org/ns size"`
	FileID        *string          `xml:"http://owncloud.org/ns fileid"`
}

type davResourceType struct {
	Collection *struct{} `xml:"DAV: collection"`
}

// parseMultistatus reads a PROPFIND reply into entries, in the server's
// order. A response for a path outside the user's files is dropped, and so
// is one none of whose properties came back 200.
func parseMultistatus(body []byte, username string) ([]Entry, error) {
	var reply multistatus
	if err := xml.Unmarshal(body, &reply); err != nil {
		return nil, fmt.Errorf("nextcloud: the server's listing is not a WebDAV multistatus: %w", err)
	}
	entries := make([]Entry, 0, len(reply.Responses))
	for _, response := range reply.Responses {
		path, inside := pathFromHref(strings.TrimSpace(response.Href), username)
		if !inside {
			continue
		}
		// A server answers the properties it has under a 200 propstat and
		// the ones it lacks under a 404 one. Only the first are read.
		var props davProp
		found := false
		for _, propstat := range response.Propstats {
			if !strings.Contains(propstat.Status+" ", " 200 ") {
				continue
			}
			found = true
			merge(&props, propstat.Prop)
		}
		if !found {
			continue
		}
		entry := Entry{Path: path, IsFolder: props.ResourceType != nil && props.ResourceType.Collection != nil}
		// The href is the source of truth for the name: displayname can be
		// absent or, on a share, differ from what the path must use.
		if parts := PathParts(path); len(parts) > 0 {
			entry.Name = parts[len(parts)-1]
		} else {
			entry.Name = text(props.DisplayName)
		}
		entry.Size = whole(props.ContentLength)
		if entry.Size == nil {
			// A folder has no content length; Nextcloud reports its size
			// in its own property.
			entry.Size = whole(props.Size)
		}
		if stamp := text(props.LastModified); stamp != "" {
			if parsed, err := mail.ParseDate(stamp); err == nil {
				entry.Modified = &parsed
			}
		}
		if !entry.IsFolder {
			entry.ContentType = text(props.ContentType)
		}
		entry.ETag = strings.Trim(text(props.ETag), `"`)
		entry.FileID = text(props.FileID)
		entries = append(entries, entry)
	}
	return entries, nil
}

// merge copies the properties one propstat holds into the entry's set.
func merge(into *davProp, from davProp) {
	for _, pair := range []struct{ to, from **string }{
		{&into.DisplayName, &from.DisplayName}, {&into.ContentLength, &from.ContentLength},
		{&into.LastModified, &from.LastModified}, {&into.ContentType, &from.ContentType},
		{&into.ETag, &from.ETag}, {&into.Size, &from.Size}, {&into.FileID, &from.FileID},
	} {
		if *pair.from != nil {
			*pair.to = *pair.from
		}
	}
	if from.ResourceType != nil {
		into.ResourceType = from.ResourceType
	}
}

func text(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

// whole is a non-negative whole number, or nil for anything else.
func whole(value *string) *int64 {
	number, err := strconv.ParseInt(text(value), 10, 64)
	if err != nil || number < 0 {
		return nil
	}
	return &number
}

// children is a Depth-1 listing without the folder itself: folders first,
// then by name without regard to case, so a walk visits a tree in the same
// order every time.
func children(entries []Entry, folder string) []Entry {
	kept := make([]Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Path != folder {
			kept = append(kept, entry)
		}
	}
	sort.SliceStable(kept, func(left, right int) bool {
		if kept[left].IsFolder != kept[right].IsFolder {
			return kept[left].IsFolder
		}
		return strings.ToLower(kept[left].Name) < strings.ToLower(kept[right].Name)
	})
	return kept
}
