// Package schema holds the schema this tier's queries are generated against,
// and the check that it is still the schema Soloclef's migrations produce.
//
// There is no schema file and no model file in this repository: the schema
// lives in surfsense_backend/alembic, as a chain of migrations. sqlc needs one
// file, so schema.sql is produced from those migrations by
// dump_from_migrations.sh and committed — which means it is a copy, and a copy
// goes stale silently. drift_test.go is what stops that: it produces the schema
// again and fails when the two differ.
//
// A column renamed by a migration and not noticed here is not a compile error.
// It is a live route answering 500 on its next request, because both tiers read
// the same tables and only one of them has a model that would have failed
// loudly. custom/docs/go-api-tier.md, "Schema drift".
package schema

// Definition is the file the sqlc configuration names as the schema and the
// drift check compares against.
const Definition = "schema.sql"

// DumpScript produces the schema from the migrations. It needs Docker and the
// backend image, so it is not something `go test ./...` runs on its own.
const DumpScript = "dump_from_migrations.sh"

// DriftCheckVariable switches the drift check on. A nightly run sets it, and so
// does anyone reviewing a migration that touches a table this tier reads.
const DriftCheckVariable = "SOLOCLEF_API_CHECK_SCHEMA_DRIFT"
