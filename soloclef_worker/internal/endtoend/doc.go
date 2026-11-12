// Package endtoend holds the indexing tests and benchmarks that need real
// services: Postgres with the real schema, Qdrant, MinIO, Redis and the
// parser service. Nothing here runs under a plain `go test ./...`; each test
// is skipped unless the services are named in the environment, which
// scripts/index_end_to_end_test.sh does after starting them in throwaway
// containers.
package endtoend
