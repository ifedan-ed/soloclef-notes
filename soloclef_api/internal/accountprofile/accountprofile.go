// Package accountprofile serves the caller's own account: the profile and the
// settings that are the account's.
//
//	GET    /users/me                  the profile
//	PATCH  /users/me                  its display name and picture
//	DELETE /users/me                  deactivate, then erase
//	GET    /api/v1/users/me/settings  chat instructions, citations, API access
//	PATCH  /api/v1/users/me/settings  change any of the three (settings_route.go)
//
// The caller's own id is the whole predicate on every one of them, so these
// routes cannot answer about anybody else. Five things about the first three
// are worth reading before the routes.
//
// **The read is the one route of the three a gated credential may use.** It
// depends on `get_auth_context`, so an API key and an agent token both reach
// it, and both are shown the account's address, its `is_superuser` flag.
// That is the account's own data, read with a credential
// the account minted, and it is recorded here rather than changed — but it is
// the reason the other two are behind `require_session_context`, and it is
// worth knowing that a leaked key reads the owner's address.
//
// **Nothing here renders another account's anything.** The field that would
// have been a leak is the address, and it is the caller's own: UserRead is
// built from one row, found by the caller's own id. The community prompt list's
// defect — every publisher's address sent to every signed-in account — has no
// equivalent here, which was checked field by field rather than assumed.
//
// **`UserRead.email` was re-parsed on every read and is not any more.** That
// model declared fastapi-users' `EmailStr`, and a response model is validated
// on the way out, so every profile read ran `email_validator` over the stored
// address and answered **500** for one it refused — a reserved domain, a label
// too long. The address is already checked where it is written, so the refusal
// could only fire for a row written outside the API and could only cost a
// parse for one written through it. It is a plain `str` on both tiers as of
// 2026-11-09; the alternative was a mirror of that library deciding a status,
// which is a parser and not a mirror.
//
// **The update is fastapi-users' `UserManager.update(safe=True)` and almost
// none of it is reachable.** That method's interesting branches are the email
// change and the password hash; `UserUpdate` deliberately declares neither
// (app/schemas/users.py says why), so what is left is
// `model_dump(exclude_unset=True)` over two nullable text columns, an
// `on_after_update` hook this deployment does not override, and a commit. The
// model is also the API's first with `extra="forbid"`, which is how a client
// that sends `email` or `password` learns it was not changed
// (internal/requestbody.RefuseExtras).
//
// **The delete is two transactions and a compensation, like the document
// delete.** It deactivates the account, signs every device out,
// commits, and *then* enqueues the erasure — putting `is_active` back and
// answering 503 if the enqueue fails, because an account locked out of a
// deletion that never runs is worse than one that is still signed in. That is
// not a `database.Effects` case: an effect runs after a commit and cannot fail
// the request, and this one has to.
package accountprofile

import (
	"time"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/requestbody"
)

// The three paths, as the Python router mounts them — which is **not** under
// /api/v1.
//
// `app.include_router(users_router)` takes no prefix, so these three live at
// the application root while `app/routes/__init__.py`'s router is mounted at
// /api/v1. So `/users/me/memory` and `/users/me` are two different
// prefixes for the same account, and this is the first route this tier serves
// outside /api/v1 at all — which the proxy matchers have to say in their own
// block, because the `/api/v1/*` directive they sit above does not cover
// these (custom/docs/go-api-tier.md).
//
// Read off the running tier's own /openapi.json rather than off the router,
// because the first version of this slice assumed the prefix and the
// comparison answered 404 on the Python tier for all three.
const (
	ReadPath   = "/users/me"
	UpdatePath = "/users/me"
	DeletePath = "/users/me"
)

// The two columns UserUpdate declares, which are also the two
// RefuseExtras allows.
var updatableFields = []string{"display_name", "avatar_url"}

// The refusals, word for word.
const (
	// sessionRequired is require_session_context's, shared with the key list
	// and the account's own memory. The read is **not** behind it.
	sessionRequired = "This action requires an interactive session"
	// queueFailed is the delete's 503, for an erasure that could not be
	// queued.
	queueFailed = "Could not start account deletion. Please try again."
)

// EraseTask is the job the delete enqueues, run by the Go worker
// (soloclef_worker/internal/accounterasure). It is not a heavy task and
// does not ignore its result, so it goes on the default queue with
// Celery's own defaults for everything else (soloclef_celery/routing).
const EraseTask = "erase_account"

// The line each route writes to the log before its sanitised 500.
const (
	readUnserved   = "the account profile could not be served"
	updateUnserved = "the account profile could not be updated"
	deleteUnserved = "the account could not be deactivated"
)

// Profile is a `"user"` row as UserRead renders it.
type Profile struct {
	ID    string
	Email string
	// Active, Superuser and Verified are the three flags BaseUser declares.
	Active      bool
	Superuser   bool
	Verified    bool
	DisplayName *string
	AvatarURL   *string
}

// Changes is a validated UserUpdate: whether each field was mentioned, and
// what it carried.
//
// Both are requestbody.MaybeText rather than pointers, because this model is
// read with `model_dump(exclude_unset=True)` and the two are different writes:
// an omitted field is not written at all while a field mentioned as null
// writes NULL, which both columns accept.
type Changes struct {
	DisplayName requestbody.MaybeText
	AvatarURL   requestbody.MaybeText
}

// Mentions reports whether the body asked for any column to be written. A body
// that mentions nothing emits no UPDATE on either tier.
func (c Changes) Mentions() bool {
	return c.DisplayName.Present || c.AvatarURL.Present
}

// Deactivation is what the delete writes before it enqueues: the instant every
// refresh token is stamped with.
type Deactivation struct {
	At time.Time
}
