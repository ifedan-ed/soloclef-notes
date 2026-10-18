import assert from "node:assert/strict";
import test from "node:test";

import { concurrencyLabel } from "@/lib/automations/concurrency-options";
import { describeRunError, isRunInFlight } from "@/lib/automations/run-outcome";

test("only a run that is waiting or running can be cancelled", () => {
	assert.equal(isRunInFlight("pending"), true);
	assert.equal(isRunInFlight("running"), true);
	for (const status of [
		"succeeded",
		"failed",
		"needs_attention",
		"cancelled",
		"timed_out",
	] as const) {
		assert.equal(isRunInFlight(status), false, status);
	}
});

test("a timed-out run reads as hitting its limit, a warning rather than a failure", () => {
	assert.deepEqual(describeRunError("TimedOut"), { title: "Hit its time limit", tone: "warning" });
	assert.deepEqual(describeRunError("StepTimedOut"), {
		title: "A task ran out of time",
		tone: "warning",
	});
});

test("a cancelled run is muted, not an error", () => {
	assert.deepEqual(describeRunError("Cancelled"), { title: "Cancelled", tone: "muted" });
});

test("an unknown error type is shown as-is; none falls back", () => {
	assert.deepEqual(describeRunError("ValueError"), { title: "ValueError", tone: "destructive" });
	assert.equal(describeRunError(undefined).title, "Run failed");
	assert.equal(describeRunError(undefined, "Error").title, "Error");
});

test("the automation page names the If-already-running choice like the form", () => {
	assert.equal(concurrencyLabel("drop_if_running"), "Skip if already running");
	assert.equal(concurrencyLabel("queue"), "Queue the next run");
	assert.equal(concurrencyLabel("always"), "Always run");
});
