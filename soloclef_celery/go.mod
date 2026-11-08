// The Celery wire protocol, shared by the two Go tiers that speak it.
//
// It is a module of its own, required with a relative replace by both
// soloclef_worker and soloclef_api, because both of them now have to agree
// with Python's Celery down to the byte and a second copy of this code is a
// second spelling (custom/docs/go-api-tier.md, "Where the encoder lives").
//
// It has no dependencies, on purpose: the two tiers reach Redis with different
// clients (go-redis and valkey-go), so putting a Redis call in here would make
// one of them carry the other's. The one call a producer needs is a one-method
// interface each tier satisfies in five lines (produce.Broker).
module git.danvics.com/danvics/soloclef/soloclef_celery

go 1.24.4
