package broker

import (
	"strconv"
	"time"
)

// taskEnvelope is a protocol 2 envelope for one task name, shaped like the one
// captured from this host in internal/celery's tests.
func taskEnvelope(tag, queue, task string) []byte {
	return buildEnvelope(tag, queue, task, 0, "null", "null", "")
}

// scheduledEnvelope carries an eta, an expires, or both.
func scheduledEnvelope(tag, queue, eta string, expires time.Time) []byte {
	etaField := "null"
	if eta != "" {
		etaField = `"` + eta + `"`
	}
	expiresField := "null"
	if !expires.IsZero() {
		expiresField = `"` + expires.UTC().Format(time.RFC3339Nano) + `"`
	}
	return buildEnvelope(tag, queue, "probe.task", 0, etaField, expiresField, "")
}

// stampedEnvelope carries this app's enqueue stamp, which is a monotonic clock
// reading rather than a wall clock time (app/celery_app.py).
func stampedEnvelope(tag, queue string, enqueuedAt time.Duration) []byte {
	return buildEnvelope(tag, queue, "probe.task", 0, "null", "null", strconv.FormatInt(int64(enqueuedAt), 10))
}

func buildEnvelope(tag, queue, task string, priority int, eta, expires, enqueuedAtNs string) []byte {
	stamp := ""
	if enqueuedAtNs != "" {
		stamp = `,"surfsense.enqueued_at_ns":"` + enqueuedAtNs + `"`
	}
	return []byte(`{"body":"W1tdLHt9LHsiY2FsbGJhY2tzIjogbnVsbH1d","content-encoding":"utf-8",` +
		`"content-type":"application/json","headers":{"lang":"py","task":"` + task + `",` +
		`"id":"` + tag + `","retries":0,"eta":` + eta + `,"expires":` + expires +
		`,"timelimit":[null,null],"root_id":"` + tag + `","parent_id":null` + stamp + `},` +
		`"properties":{"correlation_id":"` + tag + `","delivery_mode":2,"body_encoding":"base64",` +
		`"priority":` + strconv.Itoa(priority) + `,"delivery_tag":"` + tag + `",` +
		`"delivery_info":{"exchange":"` + queue + `","routing_key":"` + queue + `"}}}`)
}
