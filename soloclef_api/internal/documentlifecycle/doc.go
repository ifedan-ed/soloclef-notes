// Package documentlifecycle mounts the routes a document enters, is re-read
// and leaves by, and tests them together against real services.
//
// The routes are each a slice of their own:
//
//	GET    /api/v1/etl/capabilities                              internal/parserchoice
//	POST   /api/v1/documents/fileupload                          internal/uploads
//	POST   /api/v1/documents/folder-upload                       internal/uploads
//	POST   /api/v1/documents/{document_id}/reindex               internal/documentreindex
//	DELETE /api/v1/documents/{document_id}                       internal/documentdeletion
//	GET    /api/v1/search-source-connectors/{connector_id}/parser  internal/connectorparser
//	PUT    /api/v1/search-source-connectors/{connector_id}/parser  internal/connectorparser
//
// They are mounted from one place (Register) because they share one set of
// dependencies that must be the same object in each: the parser registry
// the capabilities route describes is the one the upload validates
// against, the limits it reports are the ones the upload enforces, and the
// queue an upload publishes to is the one a re-index publishes to. Wiring
// each route separately in cmd/api would make it possible to give two of
// them different ones. Served is the list cmd/api's servedRoutes() takes.
//
// # The opt-in test
//
// Each slice is tested on its own with stand-ins for the database, the
// queue and the stores. What those tests cannot show is that the generated
// SQL does what the stand-ins assume, that a file really lands in MinIO
// under the key its row records, that the message on Redis is one the
// worker's reader accepts, and that a delete leaves nothing in Postgres,
// Qdrant or the bucket. lifecycle_test.go shows that, through Register,
// and measures the upload and the delete while it is there.
//
// It needs four throwaway services, each under a name of its own and bound
// to loopback, removed afterwards (never the stack's own: the test empties
// the database it is given, flushes the Redis database it is given, and
// creates and drops a bucket and a collection). The database needs no
// preparing: the test applies the migrations itself (internal/testpostgres).
//
//	docker run -d --name life-pg -e POSTGRES_PASSWORD=check -e POSTGRES_DB=check \
//	    -p 127.0.0.1:39871:5432 pgvector/pgvector:pg17
//	docker run -d --name life-redis -p 127.0.0.1:39872:6379 redis:7-alpine
//	docker run -d --name life-qdrant -p 127.0.0.1:39873:6333 qdrant/qdrant:v1.19.2
//	docker run -d --name life-minio -p 127.0.0.1:39874:9000 --tmpfs /data \
//	    -e MINIO_ROOT_USER=example-access -e MINIO_ROOT_PASSWORD=example-secret \
//	    minio/minio server /data
//
//	SOLOCLEF_API_TEST_LIFECYCLE_DATABASE=postgres://postgres:check@127.0.0.1:39871/check \
//	SOLOCLEF_API_TEST_LIFECYCLE_REDIS=redis://127.0.0.1:39872/0 \
//	SOLOCLEF_API_TEST_LIFECYCLE_QDRANT=http://127.0.0.1:39873 \
//	SOLOCLEF_TEST_S3_ENDPOINT=http://127.0.0.1:39874 \
//	SOLOCLEF_TEST_S3_ACCESS_KEY=example-access SOLOCLEF_TEST_S3_SECRET_KEY=example-secret \
//	    go test ./internal/documentlifecycle/ -count=1 -v
//
//	docker rm -f life-pg life-redis life-qdrant life-minio
//
// With any of the six variables unset the test is skipped.
package documentlifecycle
