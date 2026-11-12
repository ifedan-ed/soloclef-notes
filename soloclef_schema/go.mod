// The schema of the Go version of Soloclef, and the one way it reaches a
// database: numbered SQL migrations (migrations/), applied by goose.
//
// It is a module of its own because three things need it and none of them
// owns it: the API and the two worker commands check at start-up that their
// database is at the version they were built against (schemaversion), the
// migrate command applies the files (cmd/migrate), and sqlc reads the same
// files as the schema every query is generated from (soloclef_api/sqlc.yaml).
//
// goose is pinned to a release that asks for the pgx the other modules
// already build with, so requiring this module moves nobody's driver.
module git.danvics.com/danvics/soloclef/soloclef_schema

go 1.24.4

require (
	github.com/jackc/pgx/v5 v5.7.1
	github.com/pressly/goose/v3 v3.24.1
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/mfridman/interpolate v0.0.2 // indirect
	github.com/sethvargo/go-retry v0.3.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	golang.org/x/crypto v0.31.0 // indirect
	golang.org/x/sync v0.10.0 // indirect
	golang.org/x/text v0.21.0 // indirect
)
