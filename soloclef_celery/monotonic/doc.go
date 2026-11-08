// Package monotonic reads the clock Python's time.monotonic_ns reads.
//
// It is the clock the surfsense.enqueued_at_ns header is in
// (app/celery_app.py's before_task_publish), so a producer stamps with it and
// a consumer measures queue wait against it. Both Go tiers need it for exactly
// that reason: one stamps, the other subtracts, and a stamp from any other
// clock would be reported as a measurement.
package monotonic
