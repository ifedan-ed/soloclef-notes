package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

// The settings. Each is named, none has a default, and the command says
// which one is missing: a database address written here would be one
// deployment's address shipped to every other.
const (
	// DatabaseURL is the Postgres to migrate.
	DatabaseURL = "SOLOCLEF_MIGRATE_DATABASE_URL"
	// DatabasePasswordFile names a file holding the password, for a
	// deployment that mounts its secrets as files. It is the one optional
	// setting: with it the URL must name a user and no password; without
	// it the URL is used exactly as given. A password given both ways is
	// refused, because one of the two would silently win.
	DatabasePasswordFile = "SOLOCLEF_MIGRATE_DATABASE_PASSWORD_FILE"
	// DevelopmentDatabase is read only by down-to, which destroys data. It
	// must hold the NAME of the database being rolled back, so that the
	// permission is for one database and a `true` left behind in an
	// environment file cannot empty another.
	DevelopmentDatabase = "SOLOCLEF_MIGRATE_DEVELOPMENT_DATABASE"
	// SecretKeyFile names a file holding the deployment's secret, the one
	// the services seal stored credentials with (the API's session secret
	// file; the worker's secret key file is the same file). It is read only
	// by `up` and `down-to`, and only a migration that seals or opens a
	// credential needs it: such a migration stops, naming this setting, when
	// it is not set. No other migration reads it.
	SecretKeyFile = "SOLOCLEF_MIGRATE_SECRET_KEY_FILE"
)

// secretKey is the deployment's secret from its file, or "" when the
// setting is not given. A named file that cannot be read, or is empty, is
// an error at once: a deployment that named one meant to give it.
func secretKey(env func(string) string, readFile func(string) ([]byte, error)) (string, error) {
	file := strings.TrimSpace(env(SecretKeyFile))
	if file == "" {
		return "", nil
	}
	content, err := readFile(file)
	if err != nil {
		return "", fmt.Errorf("%s: %w", SecretKeyFile, err)
	}
	// The services strip the whole of the surrounding whitespace from this
	// file (soloclef_api/internal/config), so the same bytes are the key here.
	key := strings.TrimSpace(string(content))
	if key == "" {
		return "", fmt.Errorf("%s names %s, which is empty", SecretKeyFile, file)
	}
	return key, nil
}

// databaseURL returns the address to connect to, with the password from its
// file written in when a file is named. No error quotes the URL: it may
// carry a password, and an error reaches a log.
func databaseURL(env func(string) string, readFile func(string) ([]byte, error)) (string, error) {
	address := strings.TrimSpace(env(DatabaseURL))
	if address == "" {
		return "", fmt.Errorf("%s is not set: give the Postgres address to migrate (postgres://user@host:5432/name)", DatabaseURL)
	}
	file := strings.TrimSpace(env(DatabasePasswordFile))
	if file == "" {
		return address, nil
	}
	parsed, err := url.Parse(address)
	if err != nil || parsed.User == nil || parsed.User.Username() == "" {
		return "", fmt.Errorf("%s must be a URL that names a user (postgres://user@host:5432/name) when %s is set",
			DatabaseURL, DatabasePasswordFile)
	}
	if _, inline := parsed.User.Password(); inline {
		return "", fmt.Errorf("%s carries a password and %s is set: give the password once", DatabaseURL, DatabasePasswordFile)
	}
	content, err := readFile(file)
	if err != nil {
		return "", fmt.Errorf("%s: %w", DatabasePasswordFile, err)
	}
	// A secret file usually ends in the newline its editor added.
	password := strings.TrimRight(string(content), "\r\n")
	if password == "" {
		return "", fmt.Errorf("%s names %s, which is empty", DatabasePasswordFile, file)
	}
	parsed.User = url.UserPassword(parsed.User.Username(), password)
	return parsed.String(), nil
}

// allowDown is the guard on down-to: the setting must name the database the
// command is connected to.
func allowDown(env func(string) string, connectedTo string) error {
	named := strings.TrimSpace(env(DevelopmentDatabase))
	switch {
	case named == "":
		return fmt.Errorf("down-to deletes data and is for a development database only: to allow it, set %s to the "+
			"name of the database (this one is %q)", DevelopmentDatabase, connectedTo)
	case named != connectedTo:
		return fmt.Errorf("%s names %q and this database is %q: nothing was rolled back", DevelopmentDatabase, named, connectedTo)
	}
	return nil
}

var errUsage = errors.New(`usage:
  migrate up                 apply every migration this database does not have
  migrate status             list the migrations and when each was applied
  migrate down-to <version>  roll back to <version> (development databases only; deletes data)`)

func readFile(path string) ([]byte, error) { return os.ReadFile(path) }
