package uploads

import (
	"crypto/sha256"
	"encoding/hex"

	"git.danvics.com/danvics/soloclef/soloclef_filestore"
)

// Identity is what makes two uploads the same document: the file's name in
// its owner's account, as sha256 of "FILE:{name}:{user_id}". It is written
// to `documents.unique_identifier_hash`, which is unique per account, so
// the owner is in the hash only to keep one account's value from being
// guessable from another's file name; the column's constraint, not the
// hash, is what keeps two accounts' documents apart.
func Identity(filename, userID string) string {
	sum := sha256.Sum256([]byte("FILE:" + filename + ":" + userID))
	return hex.EncodeToString(sum[:])
}

// OriginalKey is the storage key for a document's uploaded original. The
// key's shape is the file store's (filestore.OriginalKey says why it is what
// it is): the worker stores originals too, and both must write one shape.
func OriginalKey(userID string, documentID int64, filename string) (string, error) {
	return filestore.OriginalKey(userID, documentID, filename)
}
