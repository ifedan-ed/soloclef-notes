package gatedanalytics

import (
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/posthogcapture"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

// Properties renders the event's properties in the order the Python tier builds
// them: capture_for's two, then the middleware's four, then the sender's own
// (internal/posthogcapture).
func Properties(event Event) *pythonjson.Object {
	properties := posthogcapture.NewProperties()
	properties.Set("auth_method", event.AuthMethod)
	properties.Set("client", event.Client)
	properties.Set("route", event.Route)
	properties.Set("method", event.Method)
	properties.Set("status_code", event.Status)
	return posthogcapture.StampSender(properties)
}
