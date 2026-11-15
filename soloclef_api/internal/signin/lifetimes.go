package signin

import "time"

// How long each part of a session lives.
//
// They are constants, not settings: they are properties of the product (how
// long someone stays signed in), the same on every deployment, and a setting
// with a default is a second place to look for the value in force.
const (
	// accessLifetime is an access token's. Ending its sign-in ends it at
	// once (the gate checks the sign-in on every request), so the hour is
	// not a security bound; it is how often a client proves its refresh
	// token, which is what catches a copied one (rotation, refresh_tokens.go).
	accessLifetime = time.Hour
	// webIdle is how long a browser's refresh token lives unused. Every
	// refresh starts it again, so a person who comes back weekly stays
	// signed in.
	webIdle = 14 * 24 * time.Hour
	// webAbsolute caps a browser's sign-in from the moment of the sign-in,
	// however often it refreshes.
	webAbsolute = 30 * 24 * time.Hour
	// phoneIdle is how long a phone's refresh token lives unused, and the
	// only thing that ends a phone's sign-in by time. The owner's rule
	// (2026-11-13): the app "should sign out after 6 months if unused ... but
	// if always used no need for ever signing out". So every refresh starts
	// the 180 days again and there is no cap from the sign-in: a phone in use
	// stays signed in, and one given away or left in a drawer ends half a
	// year after it was last used. What ends one sooner is unchanged and
	// immediate: signing out, a device signed out from the list, signing out
	// everywhere, a new password, and a replayed refresh token.
	phoneIdle = 180 * 24 * time.Hour
	// ssoProofWindow is how recently the identity provider must have proved
	// a sign-in for an account with no password to set one from it
	// (password_change.go): long enough to sign in and type a password,
	// short enough that a phone left unlocked later is no way in.
	ssoProofWindow = 10 * time.Minute
	// rotationGrace is how soon after a rotation the spent token may be
	// presented again without that being taken for theft (rotation.go).
	rotationGrace = 45 * time.Second
)

// The two kinds of sign-in, as refresh_tokens.kind stores them.
const (
	kindWeb   = "web"
	kindPhone = "app"
)

// signInCap is when a sign-in of this kind started at `start` must end
// however often it refreshes: a browser's is webAbsolute from the sign-in. A
// phone's has none, and nil says so; it is stored as a NULL absolute_expiry,
// which every reader of the column (the gate, the devices list, the pushes)
// already takes as "no cap". A far-future date would be a second, made-up
// rule to keep in step with this one.
func signInCap(kind string, start time.Time) *time.Time {
	if kind == kindPhone {
		return nil
	}
	end := start.Add(webAbsolute)
	return &end
}

// idleLifetime is how long a refresh token of this kind lives unused.
func idleLifetime(kind string) time.Duration {
	if kind == kindPhone {
		return phoneIdle
	}
	return webIdle
}

// tokenExpiry is when a refresh token minted now stops working. Both kinds
// slide with use (each refresh mints a token that lives its idle time from
// now); a token never outlives its sign-in's cap, where it has one.
func tokenExpiry(kind string, now time.Time, familyCap *time.Time) time.Time {
	idle := now.Add(idleLifetime(kind))
	if familyCap != nil && idle.After(*familyCap) {
		return *familyCap
	}
	return idle
}
