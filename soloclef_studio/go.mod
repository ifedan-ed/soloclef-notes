// What the Studio's makers share across tiers: the client for the sandbox
// that builds and renders a deliverable (sandbox), and the rows an artifact
// is saved as (artifactsave).
//
// A module of its own because two tiers make deliverables and `internal`
// cannot cross a module: the API's chat builds documents in the sandbox and
// saves them, and the worker renders a video in the sandbox and saves it. A
// second copy of either would be a second idea of whose sandbox is whose,
// or of what an artifact's rows are.
module git.danvics.com/danvics/soloclef/soloclef_studio

go 1.24.4

require (
	git.danvics.com/danvics/soloclef/soloclef_filestore v0.0.0
	github.com/jackc/pgx/v5 v5.7.1
)

require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/go-ini/ini v1.67.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.18.2 // indirect
	github.com/klauspost/cpuid/v2 v2.2.11 // indirect
	github.com/klauspost/crc32 v1.3.0 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/minio/crc64nvme v1.1.1 // indirect
	github.com/minio/md5-simd v1.1.2 // indirect
	github.com/minio/minio-go/v7 v7.0.98 // indirect
	github.com/philhofer/fwd v1.2.0 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/rs/xid v1.6.0 // indirect
	github.com/tinylib/msgp v1.6.1 // indirect
	go.yaml.in/yaml/v3 v3.0.4 // indirect
	golang.org/x/crypto v0.46.0 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/text v0.32.0 // indirect
)

// The file store an artifact's files are written to, from this checkout for
// the reason every tier takes it from there: the tiers must agree on what a
// key means.
replace git.danvics.com/danvics/soloclef/soloclef_filestore => ../soloclef_filestore
