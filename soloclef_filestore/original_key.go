package filestore

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path"
	"strconv"
	"strings"
)

// OriginalKey is the storage key for a document's original file:
// documents/{user_id}/{document}/original/{random}{ext}. The owner comes
// first so that one account's files are one prefix of the bucket, which is
// what erasing an account, or looking at what it stores, wants.
//
// It is here, and not with the upload route that first wrote it, because
// two tiers store originals: the API (an upload, a Nextcloud import) and
// the worker (a Google Drive sync). A second spelling of the key would be a
// second idea of where an account's files are.
//
// The last segment is 128 random bits, so a key never names an object that
// already exists and a re-upload never overwrites what a reader may be
// streaming. The extension is kept (lower case) because it is useful to a
// person reading a bucket listing; nothing else of the name reaches the
// key, so a name cannot steer the path. The parser is chosen from the
// row's file name, never from the key.
func OriginalKey(userID string, documentID int64, filename string) (string, error) {
	var unique [16]byte
	if _, err := rand.Read(unique[:]); err != nil {
		return "", fmt.Errorf("filestore: no random bytes for a storage key: %w", err)
	}
	extension := strings.ToLower(path.Ext(filename))
	if !plainExtension(extension) {
		extension = ""
	}
	return "documents/" + userID + "/" + strconv.FormatInt(documentID, 10) +
		"/original/" + hex.EncodeToString(unique[:]) + extension, nil
}

// plainExtension reports whether an extension is a dot and a few letters or
// digits. Anything else (a slash, a space, a control character) is left off
// the key: the key must be one the store accepts whatever the file was
// called, and the row keeps the real name.
func plainExtension(extension string) bool {
	if len(extension) < 2 || len(extension) > 16 {
		return false
	}
	for _, letter := range extension[1:] {
		isLetter := letter >= 'a' && letter <= 'z'
		isDigit := letter >= '0' && letter <= '9'
		if !isLetter && !isDigit {
			return false
		}
	}
	return true
}
