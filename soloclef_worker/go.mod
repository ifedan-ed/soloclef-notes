module git.danvics.com/danvics/soloclef/soloclef_worker

go 1.25.0

require github.com/redis/go-redis/v9 v9.22.0

require (
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.5.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/clipperhouse/uax29/v2 v2.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/hhrutter/lzw v1.0.0 // indirect
	github.com/hhrutter/pkcs7 v0.2.0 // indirect
	github.com/hhrutter/tiff v1.0.2 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.18.2 // indirect
	github.com/klauspost/cpuid/v2 v2.2.11 // indirect
	github.com/klauspost/crc32 v1.3.0 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/minio/crc64nvme v1.1.1 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/modelcontextprotocol/go-sdk v1.8.0 // indirect
	github.com/pdfcpu/pdfcpu v0.11.1 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/tinylib/msgp v1.6.1 // indirect
	github.com/valkey-io/valkey-go v1.0.69 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/crypto v0.46.0 // indirect
	golang.org/x/image v0.34.0 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/text v0.32.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)

require (
	git.danvics.com/danvics/soloclef/soloclef_agent v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_assistant v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_automations v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_celery v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_chunker v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_connectors v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_filestore v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_ingest v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_schema v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_studio v0.0.0
	github.com/google/uuid v1.6.0
	github.com/jackc/pgx/v5 v5.7.1
	github.com/minio/minio-go/v7 v7.0.98
)

// The encoder is in this repository and deployed from this checkout, so it is
// taken from the tree rather than from a tagged version: a tag would let the
// two tiers publish different bytes for the length of a version bump, which is
// the one thing the byte comparison exists to prevent.
replace git.danvics.com/danvics/soloclef/soloclef_celery => ../soloclef_celery

// The three modules the indexing worker (cmd/indexer) is assembled from, each
// taken from this checkout for the reason above: the file store an upload was
// written to, the parsers, and the chunker. The chunker is cgo over a Rust
// static library (soloclef_chunker/AGENTS.md), so cmd/indexer needs a C
// toolchain and that library built; cmd/worker imports none of the three and
// still builds with CGO_ENABLED=0.
replace git.danvics.com/danvics/soloclef/soloclef_chunker => ../soloclef_chunker

replace git.danvics.com/danvics/soloclef/soloclef_filestore => ../soloclef_filestore

replace git.danvics.com/danvics/soloclef/soloclef_ingest => ../soloclef_ingest

// The schema: both commands check at start-up that the database is at the
// version they were built against (schemaversion), and that version is the
// migrations in this checkout. The package they import needs pgx and
// nothing else, so cmd/worker still builds with CGO_ENABLED=0.
replace git.danvics.com/danvics/soloclef/soloclef_schema => ../soloclef_schema

// The model vocabulary and the OpenAI-compatible client the API tier's chat
// already calls the proxy with (soloclef_agent/model). The podcast draft and
// the notebook naming ask the same proxy the same way, so they use the same
// client rather than a second one. It needs pgx only for its checkpoint
// package, which this module does not import.
replace git.danvics.com/danvics/soloclef/soloclef_agent => ../soloclef_agent

// The sandbox client and the artifact saver the video render uses, shared
// with the API tier's chat (which builds documents in the same sandboxes and
// saves the same rows). From this checkout for the reason the encoder is: two
// tiers writing one kind of row must not be built against two versions of
// the writer. It needs pgx and the file store, both already required, so
// cmd/worker still builds with CGO_ENABLED=0.
replace git.danvics.com/danvics/soloclef/soloclef_studio => ../soloclef_studio

// How a connection's stored credential is sealed and how a Google Drive
// grant is renewed, shared with the API tier that writes the connection's
// row (internal/drivesync reads it). From this checkout for the reason
// soloclef_celery is.
replace git.danvics.com/danvics/soloclef/soloclef_connectors => ../soloclef_connectors

// What a saved automation may say, when its schedule fires and how a run is
// created, shared with the API tier that saves one. From this checkout for
// the reason soloclef_celery is: the worker must run what the API accepted.
replace git.danvics.com/danvics/soloclef/soloclef_automations => ../soloclef_automations

// The chat's tools and the assembly of a turn's agent: an automation's run
// is one headless turn of the agent the API tier's chat answers with. It
// brings the MCP client and valkey-go, both pure Go, so cmd/worker still
// builds with CGO_ENABLED=0. Its go directive is why this module's is 1.25.
replace git.danvics.com/danvics/soloclef/soloclef_assistant => ../soloclef_assistant
