// Package posthogcapture posts product events where posthog-python posts them,
// so an event this tier sends and an event the Python tier sends are the same
// row in PostHog.
//
// It exists because phase 5 needed a second kind of event. Until the first
// write, the only event this tier sent was the gated-request event one
// middleware records (internal/gatedanalytics), and the queue that posts it was
// part of that package. A write has an event of its own — the Python route
// calls app.observability.analytics.posthog.capture_for itself, after its commit
// — so the transport and the event parted company: this package is the queue
// and the envelope, and a package per event names what it sends.
//
// The transport's shape is posthog-python's consumer: events are queued and
// posted in batches by one background worker, so a request never waits on
// PostHog and never fails because of it, and a full queue drops the event
// rather than blocking the route that reported it.
package posthogcapture

import "git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"

// LibraryName is what this tier calls itself in PostHog's $lib and in the
// User-Agent it posts with. The Python tier sends "posthog-python" and that
// library's version, which describe the library that sent the event; this tier
// has no such library, and borrowing the name would make a Go-sent event
// indistinguishable from a Python-sent one while being untrue. Everything a
// PostHog query reads is identical either way.
const LibraryName = "soloclef-api"

// backendSource is posthog.py's _SOURCE, the first property of every event it
// sends: it tells a backend event from one the web app sent.
const backendSource = "backend"

// Captured is one event, ready to post: the name a query reads, the person it
// is attributed to, and its properties in the order the Python tier builds
// them.
type Captured struct {
	Name string
	// Account is str(user.id), which is the distinct id the web app identifies
	// the same person with, so the two merge.
	Account    string
	Properties *pythonjson.Object
}

// NewProperties opens an event's properties with the one property
// posthog.py's capture() puts before the caller's own.
func NewProperties() *pythonjson.Object {
	properties := pythonjson.NewObject()
	properties.Set("source", backendSource)
	return properties
}

// StampSender closes an event's properties with the three the client library
// adds for itself. $geoip_disable is not decoration: without it PostHog
// resolves the *server's* address and overwrites each person's real location,
// which is why every event the Python tier sends carries it.
func StampSender(properties *pythonjson.Object) *pythonjson.Object {
	properties.Set("$lib", LibraryName)
	properties.Set("$geoip_disable", true)
	properties.Set("$is_server", true)
	return properties
}
