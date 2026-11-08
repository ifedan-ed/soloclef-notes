// Package gatedanalytics counts an API key's and an agent's traffic in PostHog,
// as app.py's PatApiAnalyticsMiddleware does.
//
// The Python tier captures a pat_api_request event for every request whose auth
// context is gated — an API key or an agent sign-in — with the route template,
// the method, the status and which client sent it. The three moved routes were
// missing from it, so that traffic was invisible: a reporting gap rather than a
// behavioural one, which is why nothing broke while it was open.
//
// What the two tiers send has to agree in the properties that are read, because
// a PostHog query spans both: the event name, the person it is attributed to,
// and source, auth_method, client, route, method and status_code. The
// properties that describe the *sender* name this tier rather than the Python
// library, because claiming to be that library from here would be a falsehood
// in the data (see Properties).
package gatedanalytics

// EventName is the event app.py captures.
const EventName = "pat_api_request"

// Event is one gated request, in the shape the Python middleware records it.
type Event struct {
	// Account is the person the event is attributed to, which PostHog merges
	// onto the same person the web app identifies.
	Account string
	// Route is the route template rather than the path, so the property stays
	// low-cardinality — "/api/v1/workspaces/{workspace_id}/members".
	Route  string
	Method string
	Status int
	// AuthMethod is the principal's method: "pat" or "agent".
	AuthMethod string
	// Client is "mcp" when the MCP server sent it, else "pat_script".
	Client string
}
