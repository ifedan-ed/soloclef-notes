// The chat's agent, as more than one tier builds it: the tools a turn may be
// given (the document search, the web and academic sources, the page reader,
// an account's connected apps), and the assembly that turns "this account,
// this scope, these choices" into one agent with its prompt (assembly).
//
// A module of its own because two tiers run that agent and `internal` cannot
// cross a module: the API answers a chat turn with it, and the worker runs an
// automation as one headless turn of the very same assembly. A second copy
// would be a second idea of which tools a scope offers, what the prompt
// says, or which model answers an account.
module git.danvics.com/danvics/soloclef/soloclef_assistant

go 1.25.0

require (
	git.danvics.com/danvics/soloclef/soloclef_agent v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_celery v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_filestore v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_ingest v0.0.0
	git.danvics.com/danvics/soloclef/soloclef_studio v0.0.0
	github.com/JohannesKaufmann/html-to-markdown/v2 v2.5.0
	github.com/jackc/pgx/v5 v5.7.1
	github.com/modelcontextprotocol/go-sdk v1.8.0
	github.com/valkey-io/valkey-go v1.0.69
	golang.org/x/net v0.48.0
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
	git.danvics.com/danvics/soloclef/soloclef_connectors v0.0.0
	github.com/JohannesKaufmann/dom v0.2.0 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.18.2 // indirect
	github.com/klauspost/cpuid/v2 v2.2.11 // indirect
	github.com/klauspost/crc32 v1.3.0 // indirect
	github.com/minio/crc64nvme v1.1.1 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/minio/minio-go/v7 v7.0.98 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/tinylib/msgp v1.6.1 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/crypto v0.46.0 // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
	golang.org/x/time v0.15.0 // indirect
)

// The agent loop, from this checkout for the reason every tier takes it from
// there: the tiers must run the same loop.
replace git.danvics.com/danvics/soloclef/soloclef_agent => ../soloclef_agent

// What the artifact specialist (deliverables) is made of, from this
// checkout for the reason the agent loop is: the queue message a video is
// published as, the store an artifact's files go to, and the sandbox and
// rows an artifact is built in and saved as. Every tier that runs the
// specialist must write the same bytes and the same rows.
replace git.danvics.com/danvics/soloclef/soloclef_celery => ../soloclef_celery

replace git.danvics.com/danvics/soloclef/soloclef_filestore => ../soloclef_filestore

replace git.danvics.com/danvics/soloclef/soloclef_studio => ../soloclef_studio

replace git.danvics.com/danvics/soloclef/soloclef_connectors => ../soloclef_connectors

// The parsers and the vision client the chat's file tools read a file with
// (soloclef_ingest/fileread): the very routing an upload is read with, so a
// file reads the same in a chat as in the library. The chunker is only
// soloclef_ingest's own test dependency, named so the module graph closes.
replace git.danvics.com/danvics/soloclef/soloclef_ingest => ../soloclef_ingest

replace git.danvics.com/danvics/soloclef/soloclef_chunker => ../soloclef_chunker
