// Soloclef's file store: every durable blob, by key, on local disk or in an
// S3 bucket (MinIO).
//
// A module of its own for the reason soloclef_chunker and soloclef_celery are:
// more than one Go tier needs it. The API streams files out of it and stores
// what the chat saves; the worker reads an uploaded original to index it.
// `internal` cannot cross a module, and a second copy of a store client is a
// second idea of what a key means.
module git.danvics.com/danvics/soloclef/soloclef_filestore

go 1.24.4

require github.com/minio/minio-go/v7 v7.0.98

require (
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/klauspost/compress v1.18.2 // indirect
	github.com/klauspost/cpuid/v2 v2.2.11 // indirect
	github.com/klauspost/crc32 v1.3.0 // indirect
	github.com/minio/crc64nvme v1.1.1 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/pmezard/go-difflib v1.0.0 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/tinylib/msgp v1.6.1 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/crypto v0.46.0 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sys v0.39.0
	golang.org/x/text v0.32.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)
