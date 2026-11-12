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
	// phoneLifetime is a phone's sign-in. It has no idle limit: a phone left
	// in a drawer for a month is still signed in when it is picked up, and
	// the person can sign it out from the devices list.
	phoneLifetime = 365 * 24 * time.Hour
	// rotationGrace is how soon after a rotation the spent token may be
	// presented again without that being taken for theft (rotation.go).
	rotationGrace = 45 * time.Second
)

// The two kinds of sign-in, as refresh_tokens.kind stores them.
const (
	kindWeb   = "web"
	kindPhone = "app"
)

// familyLifetime is how long a sign-in of this kind lives from its start.
func familyLifetime(kind string) time.Duration {
	if kind == kindPhone {
		return phoneLifetime
	}
	return webAbsolute
}

// tokenExpiry is when a refresh token minted now stops working: a browser's
// slides with use and never passes its family's cap; a phone's lives as long
// as its family.
func tokenExpiry(kind string, now, familyCap time.Time) time.Time {
	if kind == kindPhone {
		return familyCap
	}
	idle := now.Add(webIdle)
	if idle.After(familyCap) {
		return familyCap
	}
	return idle
}
