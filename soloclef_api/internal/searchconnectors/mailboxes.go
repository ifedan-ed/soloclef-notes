package searchconnectors

import (
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// This file is app/connectors/email_imap/credentials.py's public_config: the
// one shape a mailbox takes on its way out of the API.
//
// A stored account carries its password or its Microsoft token as Fernet
// ciphertext. Neither is returned, to anyone, the owner included -- the
// Python module says why: a ciphertext copied out of a response could be
// written into another connection, and the server would decrypt it there. So
// this is a choice of nine keys and involves no cipher.

// publicMailboxConfig is public_config(config): the same object with
// `accounts`, when it has that key, replaced by the public view of each
// entry that is an object. validated() has already established that the key
// is absent, null, or a list of objects -- and null comes back as an empty
// list.
func publicMailboxConfig(config *pythonjson.Object) *pythonjson.Object {
	shown := pythonjson.NewObject()
	for _, key := range config.Keys() {
		value, _ := config.Get(key)
		shown.Set(key, value)
	}
	if _, present := config.Get("accounts"); !present {
		return shown
	}
	public := []any{}
	if accounts, isList := config.GetOrNil("accounts").([]any); isList {
		for _, entry := range accounts {
			if account, isAccount := entry.(*pythonjson.Object); isAccount {
				public = append(public, publicAccount(account))
			}
		}
	}
	shown.Set("accounts", public)
	return shown
}

// publicAccount is public_account(account): nine keys, in this order, each
// read with `.get` -- so a missing one is null, except the three with a rule
// of their own.
func publicAccount(account *pythonjson.Object) *pythonjson.Object {
	shown := pythonjson.NewObject()
	shown.Set("id", account.GetOrNil("id"))
	shown.Set("label", account.GetOrNil("label"))
	shown.Set("host", account.GetOrNil("host"))
	shown.Set("port", account.GetOrNil("port"))
	// `.get("use_tls", True)`: true when the key is missing, and whatever is
	// stored -- a null included -- when it is there.
	if useTLS, present := account.Get("use_tls"); present {
		shown.Set("use_tls", useTLS)
	} else {
		shown.Set("use_tls", true)
	}
	shown.Set("username", account.GetOrNil("username"))
	shown.Set("created_at", account.GetOrNil("created_at"))
	// `.get("auth") or "password"`: the stored value when it is truthy, of
	// whatever type it is.
	if auth := account.GetOrNil("auth"); truthy(auth) {
		shown.Set("auth", auth)
	} else {
		shown.Set("auth", "password")
	}
	shown.Set("needs_reconnect", truthy(account.GetOrNil("needs_reconnect")))
	return shown
}

// containsText is Python's `needle in haystack` for two strs.
func containsText(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
