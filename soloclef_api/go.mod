module git.danvics.com/danvics/soloclef/soloclef_api

go 1.24.4

require (
	github.com/danielgtaylor/huma/v2 v2.27.0
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/jackc/pgx/v5 v5.7.1
	github.com/valkey-io/valkey-go v1.0.69
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/crypto v0.28.0 // indirect
	golang.org/x/sync v0.12.0 // indirect
	golang.org/x/sys v0.31.0 // indirect
	golang.org/x/text v0.23.0 // indirect
)

require git.danvics.com/danvics/soloclef/soloclef_celery v0.0.0

// Taken from the tree rather than from a tagged version: the two tiers have to
// publish the same bytes, and a tag would let them differ for the length of a
// version bump (custom/docs/go-api-tier.md, "Where the encoder lives").
replace git.danvics.com/danvics/soloclef/soloclef_celery => ../soloclef_celery
