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
// migrate command creates.
package migrations

import "embed"

// Files holds every migration, at the root of the file system.
//
//go:embed *.sql
var Files embed.FS
