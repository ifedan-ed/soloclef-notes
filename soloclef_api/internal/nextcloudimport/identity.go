package nextcloudimport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// identity is what makes two imports the same document: the Nextcloud
// account and the path, for one owner. Not the connection row: removing
// and re-adding a connection must not duplicate what was already imported.
//
// It is sha256 of "FILE:nextcloud://{user}@{host}{path}:{user_id}", written
// to `unique_identifier_hash`, which is unique per owner: two people who
// both import the same shared Nextcloud file each get their own document.
func identity(account Account, path, userID string) string {
	host := ""
	if parsed, err := url.Parse(account.BaseURL); err == nil {
		host = strings.ToLower(parsed.Host)
	}
	sum := sha256.Sum256([]byte("FILE:nextcloud://" + account.Username + "@" + host + path + ":" + userID))
	return hex.EncodeToString(sum[:])
}

// placeholderHash fills `content_hash` (NOT NULL) for a document whose
// bytes have not been downloaded. It is derived from the identity so it is
// stable and cannot collide with a real SHA-256 of content by accident of
// two documents sharing it.
func placeholderHash(identity string) string {
	sum := sha256.Sum256([]byte("placeholder:" + identity))
	return hex.EncodeToString(sum[:])
}

// facts is the `nextcloud` block of a document's metadata: where the file
// came from and what Nextcloud said of it when it was read. The refresh
// route and the list of refreshable documents read it.
//
// The parser is not in it: it is the document's own column, and a second
// record of it could only disagree with the first.
type facts struct {
	ConnectorID int64   `json:"connector_id"`
	ServerURL   string  `json:"server_url"`
	Username    string  `json:"username"`
	Path        string  `json:"path"`
	ETag        *string `json:"etag"`
	Modified    *string `json:"modified"`
	ContentType *string `json:"content_type"`
	Size        *int64  `json:"size"`
	NotebookID  *int64  `json:"notebook_id"`
	ImportedAt  string  `json:"imported_at"`
}

// isoTime is the block's time format (ISO 8601 with an offset), the one its
// readers have always been given.
const isoTime = "2006-01-02T15:04:05.999999-07:00"

func optional(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

// metadataFor is a document's metadata for one planned file.
func metadataFor(entry Entry, account Account, connectorID int64, notebookID *int64, now time.Time) ([]byte, error) {
	block := facts{
		ConnectorID: connectorID, ServerURL: account.BaseURL, Username: account.Username, Path: entry.Path,
		ETag: optional(entry.ETag), ContentType: optional(entry.ContentType), Size: entry.Size,
		NotebookID: notebookID, ImportedAt: now.UTC().Format(isoTime),
	}
	if entry.Modified != nil {
		block.Modified = optional(entry.Modified.Format(isoTime))
	}
	return json.Marshal(map[string]any{
		"FILE_NAME": entry.Name,
		"file_size": entry.Size,
		"source":    "nextcloud",
		"nextcloud": block,
	})
}

var (
	unsafeInName = regexp.MustCompile(`[\\/:*?"<>|]+`)
	spaceRun     = regexp.MustCompile(`\s+`)
)

// The longest folder name kept, in characters and in bytes: what the
// folder tree has always allowed, and what a file system holds in one
// segment.
const (
	folderNameRunes = 180
	folderNameBytes = 255
)

// folderName is a Nextcloud folder's name as a folder here may carry
// it: the characters a path cannot hold become "_", runs of white space
// become one space, and a name that is nothing after that is "folder".
func folderName(raw string) string {
	name := strings.TrimSpace(unsafeInName.ReplaceAllString(raw, "_"))
	name = spaceRun.ReplaceAllString(name, " ")
	if name == "" || name == "." || name == ".." {
		return "folder"
	}
	if utf8.RuneCountInString(name) > folderNameRunes {
		name = strings.TrimRight(string([]rune(name)[:folderNameRunes]), " ")
	}
	for len(name) > folderNameBytes {
		// Cut on a character boundary.
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	return strings.TrimRight(name, " ")
}
