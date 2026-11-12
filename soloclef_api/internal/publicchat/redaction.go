package publicchat

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"

// internalKeys is app/public_sharing/redaction.py:INTERNAL_KEYS: the keys
// that never leave in a page's messages, wherever in a message they are.
//
// The match is on the exact key and is case-sensitive, so it is a list of
// twelve spellings and not a rule: "token" is removed and "access_token",
// "Token" and "password" are not. This is the Python tier's list, mirrored;
// widening it here would be this tier sending a different page.
var internalKeys = map[string]bool{
	"storage_key":       true,
	"audio_storage_key": true,
	"storage_backend":   true,
	"file_path":         true,
	"audio_file":        true,
	"user_id":           true,
	"created_by_id":     true,
	"author_id":         true,
	"email":             true,
	"user_email":        true,
	"api_key":           true,
	"token":             true,
}

// withoutInternalKeys is redaction.py:without_internal_keys: a copy of the
// value with every internal key removed from every object in it.
func withoutInternalKeys(value any) any {
	switch held := value.(type) {
	case *pythonjson.Object:
		kept := pythonjson.NewObject()
		for _, key := range held.Keys() {
			if internalKeys[key] {
				continue
			}
			kept.Set(key, withoutInternalKeys(held.GetOrNil(key)))
		}
		return kept
	case []any:
		kept := make([]any, 0, len(held))
		for _, item := range held {
			kept = append(kept, withoutInternalKeys(item))
		}
		return kept
	}
	return value
}

// publicAuthor is redaction.py:public_author: a display name and nothing
// else. `avatar_url` is always null -- older snapshots stored one, and an
// account id beside it -- and an author with no display name is no author.
func publicAuthor(displayName any) any {
	if displayName == nil {
		return nil
	}
	author := pythonjson.NewObject()
	author.Set("display_name", displayName)
	author.Set("avatar_url", nil)
	return author
}

// redactMessages is _redact_messages: the messages with the internal keys
// gone and each author cut down to a display name.
//
// It loops over `without_internal_keys(messages)`, whatever that is. A list
// is walked item by item; an object is walked by key and a string by
// character, and each key or character goes into the answer as it is -- to be
// refused by the response model a moment later. Only a message that is an
// object with an author that is an object has the author replaced; an author
// that is a string is left for the model to refuse.
func redactMessages(messages any) ([]any, error) {
	entries, err := iterated(withoutInternalKeys(messages))
	if err != nil {
		return nil, err
	}
	redacted := make([]any, 0, len(entries))
	for _, entry := range entries {
		message, isObject := entry.(*pythonjson.Object)
		if !isObject {
			redacted = append(redacted, entry)
			continue
		}
		author, hasObjectAuthor := message.GetOrNil("author").(*pythonjson.Object)
		if !hasObjectAuthor {
			redacted = append(redacted, message)
			continue
		}
		// {**message, "author": …}: the key keeps its place.
		replaced := pythonjson.NewObject()
		for _, key := range message.Keys() {
			replaced.Set(key, message.GetOrNil(key))
		}
		replaced.Set("author", publicAuthor(author.GetOrNil("display_name")))
		redacted = append(redacted, replaced)
	}
	return redacted, nil
}
