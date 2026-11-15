// Package migrations is the schema, as the numbered SQL files in this
// directory, compiled into every binary that needs to know it.
//
// A file is named NNNNN_what_it_does.sql and has a `-- +goose Up` section
// and a `-- +goose Down` section. Files are applied in number order and a
// number is never reused or edited once it has reached a database anyone
// keeps: a change to the schema is a new file.
//
// sqlc reads this same directory as its schema (soloclef_api/sqlc.yaml), so
// the tables a query is checked against at generate time are the tables the
// migrate command creates. A migration that changes rows in a way SQL
// cannot (sealing a stored credential) is written in Go instead and listed
// in CodedMigrations (coded.go); a coded migration never changes the schema,
// so sqlc's view of the tables stays the SQL files'.
package migrations

import "embed"

// Files holds every migration, at the root of the file system.
//
//go:embed *.sql
var Files embed.FS
