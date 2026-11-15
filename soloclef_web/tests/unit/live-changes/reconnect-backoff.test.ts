import assert from "node:assert/strict";
import { test } from "node:test";
import {
	attemptAfterConnection,
	HEALTHY_CONNECTION_MS,
	MAX_RECONNECT_DELAY_MS,
	reconnectDelayMs,
} from "@/features/live-changes/lib/reconnect-backoff";

test("with no jitter the delays double from one second", () => {
	// random 0.5 is the middle of the spread: the step itself.
	assert.deepEqual(
		[0, 1, 2, 3, 4].map((attempt) => reconnectDelayMs(attempt, 0.5)),
		[1_000, 2_000, 4_000, 8_000, 16_000]
	);
});

test("the delay never passes thirty seconds, jitter included", () => {
	for (const attempt of [5, 6, 10, 50]) {
		for (const random of [0, 0.5, 0.999]) {
			assert.ok(reconnectDelayMs(attempt, random) <= MAX_RECONNECT_DELAY_MS);
		}
	}
	assert.equal(reconnectDelayMs(10, 0.5), MAX_RECONNECT_DELAY_MS);
});

test("jitter spreads each step from 75 % to 125 %", () => {
	assert.equal(reconnectDelayMs(1, 0), 1_500);
	assert.equal(reconnectDelayMs(1, 0.999), 2_499);
});

test("a connection that lived past thirty seconds starts the delays over", () => {
	assert.equal(attemptAfterConnection(4, HEALTHY_CONNECTION_MS + 1), 0);
	assert.equal(attemptAfterConnection(4, HEALTHY_CONNECTION_MS), 4);
	assert.equal(attemptAfterConnection(4, 200), 4);
});
