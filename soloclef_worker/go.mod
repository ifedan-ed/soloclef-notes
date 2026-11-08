module git.danvics.com/danvics/soloclef/soloclef_worker

go 1.24.4

require github.com/redis/go-redis/v9 v9.22.0

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sys v0.30.0 // indirect
)

require git.danvics.com/danvics/soloclef/soloclef_celery v0.0.0

// The encoder is in this repository and deployed from this checkout, so it is
// taken from the tree rather than from a tagged version: a tag would let the
// two tiers publish different bytes for the length of a version bump, which is
// the one thing the byte comparison exists to prevent.
replace git.danvics.com/danvics/soloclef/soloclef_celery => ../soloclef_celery
