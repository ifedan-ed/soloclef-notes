package nextcloud

import (
	"errors"
	"net/url"
	"strings"
)

// Root is the user's files root.
const Root = "/"

// ErrInvalidPath is a path that is not a plain location inside the user's
// files.
var ErrInvalidPath = errors.New("nextcloud: not a path inside the user's files")

// NormalizePath turns anything a caller or a server sent into the one
// canonical form: absolute, slash-separated, no trailing slash except the
// root, and unable to climb out of the user's files.
//
// Every path goes through here before it reaches a URL. "..", a NUL and a
// backslash are refused, not repaired: a path that needs repairing is not
// one this code built.
//
// One defect of the Python version is fixed: it stripped white space from
// the ends of the whole text, so a file whose name ends in a space (legal
// in Nextcloud, and it happens) was asked for under a different name and
// answered "not found". Nothing is trimmed here.
func NormalizePath(raw string) (string, error) {
	if strings.ContainsAny(raw, "\x00\\") {
		return "", ErrInvalidPath
	}
	var parts []string
	for _, part := range strings.Split(raw, "/") {
		switch part {
		case "", ".":
		case "..":
			return "", ErrInvalidPath
		default:
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 {
		return Root, nil
	}
	return "/" + strings.Join(parts, "/"), nil
}

// PathParts is the folder and file names of a normalized path, root first.
func PathParts(path string) []string {
	var parts []string
	for _, part := range strings.Split(path, "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return parts
}

// ParentOf is the folder a normalized path is in.
func ParentOf(path string) string {
	parts := PathParts(path)
	if len(parts) <= 1 {
		return Root
	}
	return "/" + strings.Join(parts[:len(parts)-1], "/")
}

// filesRoot is the URL path of a user's files root, as the server writes it
// in an href.
func filesRoot(username string) string {
	return "/remote.php/dav/files/" + url.PathEscape(username) + "/"
}

// davURL is the absolute WebDAV URL of a normalized path. Every segment is
// escaped on its own, so a name holding "?", "#" or "%" stays a name.
func davURL(baseURL, username, path string) string {
	parts := PathParts(path)
	escaped := make([]string, len(parts))
	for index, part := range parts {
		escaped[index] = url.PathEscape(part)
	}
	return strings.TrimRight(baseURL, "/") + filesRoot(username) + strings.Join(escaped, "/")
}

// pathFromHref is the remote path an href names, and false when it is
// outside the user's files.
//
// A server may send a bare path or a full URL, and may keep a sub-path
// prefix (Nextcloud served under /nextcloud); only what follows
// /remote.php/dav/files/<user>/ matters. The user segment is compared after
// decoding, so it matches whether or not the server escaped it.
func pathFromHref(href, username string) (string, bool) {
	parsed, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	// Path is already decoded; EscapedPath is not consulted, so "%2F" in a
	// name would arrive as a slash. Nextcloud forbids a slash in a name, so
	// a decoded slash is always a separator.
	marker := "/remote.php/dav/files/" + username + "/"
	decoded := parsed.Path
	index := strings.Index(decoded, marker)
	if index < 0 {
		// The root itself may come without its trailing slash.
		if strings.HasSuffix(decoded, strings.TrimSuffix(marker, "/")) {
			return Root, true
		}
		return "", false
	}
	path, err := NormalizePath(decoded[index+len(marker):])
	if err != nil {
		return "", false
	}
	return path, true
}
