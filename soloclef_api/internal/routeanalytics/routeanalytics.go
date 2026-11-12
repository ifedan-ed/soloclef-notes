// Package routeanalytics captures a route's own product event, as
// app.observability.analytics.posthog.capture_for does.
//
// This is the second kind of PostHog event this tier sends and the first one a
// route decides on. The gated-request event is a middleware's
// (internal/gatedanalytics): it fires for every request a gated caller makes,
// whatever the route did. A write's event is the route's own — the Python route
// calls capture_for itself, with properties only it knows, after its commit has
// returned — so a Go write that skipped it would leave a hole in the same
// PostHog chart the Python route fills.
//
// What capture_for adds to a route's own properties is the person and two
// properties about the principal, and that is all this package is: the two
// stamps, in their order, in front of whatever the slice names. Where the event
// is *fired* is not here but in the write's own transaction seam
// (internal/database, Effects), because an event that is captured for a
// transaction that then rolls back is a lie.
package routeanalytics

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/posthogcapture"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Sink takes a captured event away from the request.
type Sink interface {
	Capture(posthogcapture.Captured)
}

// Capture renders and queues a route's event. A zero Capture records nothing,
// which is this deployment having no project key: posthog.py's every function
// is a no-op then, so a route must not have to know whether telemetry is on.
type Capture struct {
	Sink Sink
}

// Record captures one event for the caller. own writes the route's own
// properties, which land after capture_for's two and before the sender's.
func (c Capture) Record(caller principal.Principal, name string, own func(*pythonjson.Object)) {
	c.record(caller, name, own)
}

func (c Capture) record(
	caller principal.Principal,
	name string,
	own func(*pythonjson.Object),
) {
	if c.Sink == nil {
		return
	}

	properties := posthogcapture.NewProperties()
	properties.Set("auth_method", string(caller.Method))
	properties.Set("client", clientLabel(caller))
	if own != nil {
		own(properties)
	}
	c.Sink.Capture(posthogcapture.Captured{
		Name:       name,
		Account:    caller.UserID,
		Properties: posthogcapture.StampSender(properties),
	})
}

// clientLabel is posthog.py's _client_label, which is not the gated-request
// middleware's label: that one overrides `client` with "mcp" or "pat_script"
// from a header, and this one reads only the principal. A session is "web"
// there because the method alone cannot tell the web app from the desktop app.
func clientLabel(caller principal.Principal) string {
	switch caller.Method {
	case principal.MethodPersonalAccessToken:
		return "pat"
	case principal.MethodAgent:
		return "agent"
	default:
		return "web"
	}
}
