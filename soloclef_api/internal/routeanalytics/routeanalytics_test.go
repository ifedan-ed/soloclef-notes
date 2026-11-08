package routeanalytics

import (
	"testing"

	"git.danvics.com/danvics/soloclef/soloclef_api/internal/posthogcapture"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/principal"
	"git.danvics.com/danvics/soloclef/soloclef_api/internal/pythonjson"
)

type recorder struct {
	captured []posthogcapture.Captured
}

func (r *recorder) Capture(event posthogcapture.Captured) {
	r.captured = append(r.captured, event)
}

// The properties and their order are capture_for's: source first, then the two
// it stamps about the principal, then the route's own, then the sender's. The
// order is checked because the body is rendered in it.
func TestTheEventIsCaptureForsShape(t *testing.T) {
	sink := &recorder{}
	Capture{Sink: sink}.Record(
		principal.Principal{UserID: "aaaaaaaa-0000-4000-8000-000000000001", Method: principal.MethodSession},
		"pat_revoked",
		func(properties *pythonjson.Object) { properties.Set("pat_id", int64(7)) })

	if len(sink.captured) != 1 {
		t.Fatalf("%d events, want 1", len(sink.captured))
	}
	event := sink.captured[0]
	if event.Name != "pat_revoked" {
		t.Errorf("event %q", event.Name)
	}
	if event.Account != "aaaaaaaa-0000-4000-8000-000000000001" {
		t.Errorf("attributed to %q", event.Account)
	}

	want := []string{"source", "auth_method", "client", "pat_id", "$lib", "$geoip_disable", "$is_server"}
	got := event.Properties.Keys()
	if len(got) != len(want) {
		t.Fatalf("properties %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("property %d is %q, want %q", index, got[index], want[index])
		}
	}
}

// _client_label reads the principal alone, so a session is "web": the method
// cannot tell the web app from the desktop app. This is not the gated-request
// middleware's label, which a header decides.
func TestTheClientLabelIsThePrincipalsAlone(t *testing.T) {
	for method, want := range map[principal.Method]string{
		principal.MethodSession:             "web",
		principal.MethodPersonalAccessToken: "pat",
		principal.MethodAgent:               "agent",
	} {
		sink := &recorder{}
		Capture{Sink: sink}.Record(principal.Principal{UserID: "a", Method: method}, "e", nil)
		label, _ := sink.captured[0].Properties.Get("client")
		if label != want {
			t.Errorf("%s: client %v, want %q", method, label, want)
		}
		if got, _ := sink.captured[0].Properties.Get("auth_method"); got != string(method) {
			t.Errorf("%s: auth_method %v", method, got)
		}
	}
}

// A deployment with no project key has no sink, and a route must not have to
// know: every function of posthog.py is a no-op then.
func TestNoSinkRecordsNothingAndDoesNotPanic(t *testing.T) {
	Capture{}.Record(principal.Principal{UserID: "a", Method: principal.MethodSession}, "e", nil)
}
