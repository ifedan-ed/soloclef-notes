// Package nextcloud is an account's files in the deployment's Nextcloud,
// over WebDAV: reading a stored connection's credentials (Credentials),
// listing, searching and downloading (Client), and the two writes the
// chat may make with a person's approval (Writer).
//
// It is shared because two tiers reach the same files the same way: the
// API imports a picked file into the library (soloclef_api/internal/
// nextcloudimport), and the chat's Nextcloud tools search, read, write and
// bin files on a person's behalf (soloclef_assistant/nextcloudtools), in
// the API and in the worker's automations. One client means one idea of
// what a path is, which server an app password may be sent to, and what a
// redirect does (nothing).
//
// # What keeps the app password where it belongs
//
//   - Credentials reads a connection only when it names the one server
//     the deployment names: a row cannot point the password anywhere else.
//   - Neither the Client nor the Writer follows a redirect. A redirect to
//     another host would carry the password there (Go strips
//     Authorization across hosts, but not on a downgrade to http on the
//     same one), and a correct Nextcloud never redirects a WebDAV request.
//   - Every path goes through NormalizePath before it reaches a URL, and
//     each segment is escaped on its own, so a name cannot climb out of
//     the user's files or turn into a query.
//
// # Reading and writing are apart
//
// Client sends PROPFIND, SEARCH and GET and has no code path that writes:
// the import, which runs without anyone approving each request, holds only
// a Client. The Writer is the chat's, and each of its calls is one the
// person approved.
//
// It requires nothing outside the standard library, as the module does.
package nextcloud

import "errors"

// The failures of the remote side that a caller tells apart. Each is safe
// to describe to the person: none carries an address or a credential.
var (
	// ErrAuth: the server refused the user name or the app password.
	ErrAuth = errors.New("Nextcloud refused the username or app password")
	// ErrNotFound: the path does not exist, or this account cannot see it.
	ErrNotFound = errors.New("not found in Nextcloud")
	// ErrUnavailable: the server could not be reached, answered an error,
	// stalled, or kept asking this client to slow down.
	ErrUnavailable = errors.New("Nextcloud could not be reached")
	// ErrExists: a write would replace a file that is already there.
	ErrExists = errors.New("a file of that name is already there in Nextcloud")
	// ErrNoFolder: a write named a folder that is not there.
	ErrNoFolder = errors.New("that folder is not in Nextcloud")
)
