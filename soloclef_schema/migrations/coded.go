package migrations

import (
	"context"
	"database/sql"
	"errors"
)

// Coded is a migration written in Go rather than SQL: one that changes
// rows in a way SQL cannot, such as sealing a stored credential with the
// deployment's secret.
//
// It is described here without goose's types, so that a service, which
// only checks the version (schemaversion), does not carry the migration
// runner; the runner (the schema package) wraps each in goose's own form.
// Like a SQL file, a coded migration runs in one transaction and is never
// edited once it has reached a database anyone keeps.
type Coded struct {
	Version int64
	// Name is what the migrate command prints, in a SQL file's style.
	Name string
	Up   func(ctx context.Context, tx *sql.Tx, keys Keys) error
	Down func(ctx context.Context, tx *sql.Tx, keys Keys) error
}

// Keys are the deployment's secrets a coded migration may need.
type Keys struct {
	// SecretKey is the deployment's secret, the one the services seal
	// stored credentials with (the API's SESSION_SECRET, the worker's
	// SECRET_KEY: one file). Only a migration that seals or opens a
	// credential reads it.
	SecretKey string
}

// ErrNoSecretKey is a coded migration that seals credentials, run without
// the deployment's secret. The migrate command words it with its setting.
var ErrNoSecretKey = errors.New("migrations: this migration seals stored credentials and needs the deployment's secret key")

// CodedMigrations are the coded migrations, in version order. Their numbers
// share the sequence with the SQL files and never collide with one.
var CodedMigrations = []Coded{
	{Version: 12, Name: "00012_seal_connector_secrets", Up: sealConnectorSecrets, Down: unsealConnectorSecrets},
}
