package searchconnectors

import (
	"strings"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// This file is app/connectors/public_config.py: what of a connection's stored
// `config` a read shows.
//
// # Why there is a list at all
//
// The column is where every connection keeps its credential. A Drive grant's
// tokens and client secret and a Nextcloud app password are Fernet ciphertext
// there, under the server's SECRET_KEY, and both used to be answered as
// stored -- to the owner by id, and to every member who may read connectors
// by the list. The generic update merged whatever keys it was sent. So a
// reader could copy a colleague's ciphertext out of the list, write it into a
// connection of their own, and have the server decrypt it and act with that
// person's grant.
//
// So a read now shows only the keys named here for the connection's type,
// for every caller, the owner included; and the Python tier's generic writes
// refuse any key that is not one of them. It is a list of what is shown and
// not of what is hidden so that a credential field added later is hidden
// until someone decides otherwise.
//
// Nothing here decrypts or compares anything: it is a choice of keys.

// publicKeys is PUBLIC_KEYS. Every connector type the column's enum can hold
// has an entry, and a type without one is an error, never "show everything"
// and never "show nothing": the Python dict raises KeyError for it, inside
// the route's `try`.
var publicKeys = map[string]map[string]bool{
	// The person's picks and the reconnect flag. Not the grant.
	"GOOGLE_DRIVE_CONNECTOR": {
		"selected_folders": true, "selected_files": true,
		"indexing_options": true, "auth_expired": true,
	},
	// Nothing: the row is a login.
	"NEXTCLOUD_CONNECTOR": {},
	// The mailboxes, which mailboxes.go then reduces.
	mailboxType: {"accounts": true},
	// The page list. Not the crawler key, which was stored in the clear.
	"WEBCRAWLER_CONNECTOR": {"INITIAL_URLS": true},
	// The server settings, which secrets.go then masks; who added it; the
	// tools that run without asking; the reconnect flag. Not the cached tool
	// definitions, which are text a remote server chose.
	serverTypeTag: {
		"server_config": true, "server_origin": true,
		"trusted_tools": true, "auth_expired": true,
	},
}

// publicConfig is public_config(connector_type, config): the type's public
// keys in the order they are stored, then the one reduction the type has --
// the mask for an MCP server, the nine fields for a mailbox.
//
// A key is compared as Python compares two strs, so one holding a lone
// surrogate (surrogates.go) is simply not one of the names and is left out.
func publicConfig(connectionType string, config *pythonjson.Object) (*pythonjson.Object, error) {
	allowed, known := publicKeys[connectionType]
	if !known {
		return nil, errRowRefused
	}
	shown := pythonjson.NewObject()
	for _, key := range config.Keys() {
		if allowed[key] {
			value, _ := config.Get(key)
			shown.Set(key, value)
		}
	}
	switch connectionType {
	case serverTypeTag:
		return maskConnectorConfig(shown)
	case mailboxType:
		return publicMailboxConfig(shown), nil
	}
	return shown, nil
}

// cannotBeStored is app/utils/storable_text.py's cannot_be_stored: text
// holding U+0000 or a lone surrogate, neither of which a connection's config
// can give back once it is stored. The first makes every later statement
// that casts the column to jsonb fail; the second makes every read of the
// row, and of every list it is in, a 500.
//
// Of the two, only U+0000 can reach a route of this tier in a request: a
// body carrying a lone surrogate is handed to the Python tier by
// internal/requestbody before any route runs, and Python refuses it in the
// same words. The recorded table holds both, and the replay checks that the
// second was handed over rather than answered here.
func cannotBeStored(text string) bool {
	return strings.ContainsRune(text, 0) || hasLoneSurrogate(text)
}
