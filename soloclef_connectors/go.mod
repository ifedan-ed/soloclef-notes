// What both Go tiers need to know about an account's connections to outside
// services: how a stored credential is sealed (storedtoken), how a Google
// Drive grant is kept and renewed (drivegrant), and how an account's
// Nextcloud files are read and written over WebDAV (nextcloud).
//
// A module of its own for the reason soloclef_filestore is one: the API
// writes a connection's row when a person connects, and the worker reads the
// same row to sync it. `internal` cannot cross a module, and a second copy of
// the sealing would be a second idea of what a stored token is.
//
// It requires nothing outside the standard library.
module git.danvics.com/danvics/soloclef/soloclef_connectors

go 1.24.4
