module git.danvics.com/danvics/soloclef/soloclef_api

go 1.25.0

require (
	github.com/danielgtaylor/huma/v2 v2.27.0
	github.com/golang-jwt/jwt/v5 v5.3.1
	github.com/jackc/pgx/v5 v5.7.1
	github.com/minio/minio-go/v7 v7.0.98
	github.com/valkey-io/valkey-go v1.0.69
)

require (
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.5.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.18.2 // indirect
	github.com/klauspost/cpuid/v2 v2.2.11 // indirect
	github.com/klauspost/crc32 v1.3.0 // indirect
	github.com/minio/crc64nvme v1.1.1 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/modelcontextprotocol/go-sdk v1.8.0 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/tinylib/msgp v1.6.1 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/crypto v0.46.0
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

require (
	git.danvics.com/danvics/soloclef/soloclef_agent v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_assistant v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_celery v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_connectors v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_filestore v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_ingest v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_mail v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_schema v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_studio v0.0.0
	golang.org/x/text v0.32.0
)

require (
	github.com/clipperhouse/uax29/v2 v2.2.0 // indirect
	github.com/hhrutter/lzw v1.0.0 // indirect
	github.com/hhrutter/pkcs7 v0.2.0 // indirect
	github.com/hhrutter/tiff v1.0.2 // indirect
	github.com/mattn/go-runewidth v0.0.19 // indirect
	github.com/pdfcpu/pdfcpu v0.11.1 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	golang.org/x/image v0.34.0 // indirect
	gopkg.in/yaml.v2 v2.4.0 // indirect
)

require (
	git.danvics.com/danvics/soloclef/soloclef_automations v0.0.0
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

// Taken from the tree rather than from a tagged version: the two tiers have to
// publish the same bytes, and a tag would let them differ for the length of a
// version bump (custom/docs/go-api-tier.md, "Where the encoder lives").
replace git.danvics.com/danvics/soloclef/soloclef_celery => ../soloclef_celery

// The agent loop the chat runs on. A relative replace for the reason
// soloclef_celery is one: it is a module of its own because more than one
// tier will run agents, and this tier must build against the copy in this
// repository, not a published one.
replace git.danvics.com/danvics/soloclef/soloclef_agent => ../soloclef_agent

// The file store, shared with the worker, which reads an uploaded original to
// index it. A relative replace for the reason soloclef_celery is one.
replace git.danvics.com/danvics/soloclef/soloclef_filestore => ../soloclef_filestore

// The parsers and the per-source parser choice, shared with the indexing
// worker: the upload route must refuse exactly the parser and the file the
// worker would refuse, so it asks the same registry (selection.Registry)
// instead of keeping a second table of who reads what.
replace git.danvics.com/danvics/soloclef/soloclef_ingest => ../soloclef_ingest

// Required by soloclef_ingest (its chunk-flow test); a replace is not
// inherited from a dependency, so it is repeated here.
replace git.danvics.com/danvics/soloclef/soloclef_chunker => ../soloclef_chunker

// The schema: the migrations this tier's queries are generated from, and the
// start-up check that the database is at the version this build was made
// against (schemaversion). A relative replace for the reason soloclef_celery
// is one: the check must compare against the migrations in this checkout.
replace git.danvics.com/danvics/soloclef/soloclef_schema => ../soloclef_schema

// The sandbox client and the artifact saver, shared with the worker's video
// render. A relative replace for the reason soloclef_celery is one: both
// tiers must save the same rows and find the same sandboxes.
replace git.danvics.com/danvics/soloclef/soloclef_studio => ../soloclef_studio

// The SMTP transport the password-reset mail leaves through
// (soloclef_mail/mailer/smtp). Only that package and the message type are
// compiled in: the queue and its Redis client stay out of this tier's build
// list (soloclef_mail/AGENTS.md, "Why it is a module").
replace git.danvics.com/danvics/soloclef/soloclef_mail => ../soloclef_mail

// How a connection's stored credential is sealed, and how a Google Drive
// grant is kept and renewed: this tier writes a connection's row and the
// worker's Drive sync reads it. A relative replace for the reason
// soloclef_celery is one.
replace git.danvics.com/danvics/soloclef/soloclef_connectors => ../soloclef_connectors

// The chat's tools and the assembly of a turn's agent, shared with the worker,
// which runs an automation as one headless turn of the same assembly. A
// relative replace for the reason soloclef_celery is one: both tiers must
// offer the same tools under the same prompt.
replace git.danvics.com/danvics/soloclef/soloclef_assistant => ../soloclef_assistant

// What a saved automation may say and how a run is created, shared with
// the worker that runs one. A relative replace for the reason soloclef_celery
// is one: this tier must refuse at save time what the worker would refuse.
replace git.danvics.com/danvics/soloclef/soloclef_automations => ../soloclef_automations
