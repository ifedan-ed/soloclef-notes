import assert from "node:assert/strict";
import { test } from "node:test";
import { ChangeCoalescer } from "@/features/live-changes/lib/change-coalescer";
import type { LiveChange } from "@/features/live-changes/model/live-change";

/** Timers run by hand: `fire()` runs whatever is due. */
function manualTimers() {
	const due = new Map<number, () => void>();
	let next = 1;
	return {
		setTimer: (run: () => void) => {
			const id = next++;
			due.set(id, run);
			return id;
		},
		clearTimer: (id: unknown) => {
			due.delete(id as number);
		},
		fire: () => {
			const runs = [...due.values()];
			due.clear();
			for (const run of runs) run();
		},
		pending: () => due.size,
	};
}

test("a burst of changes becomes one hand-over with each distinct change once", () => {
	const timers = manualTimers();
	const batches: LiveChange[][] = [];
	const coalescer = new ChangeCoalescer((changes) => batches.push(changes), 250, timers);
	coalescer.add({ type: "documents", id: 1 });
	coalescer.add({ type: "documents", id: 2 });
	coalescer.add({ type: "documents", id: 1 });
	coalescer.add({ type: "inbox" });
	assert.equal(timers.pending(), 1, "one window, not one per change");
	assert.equal(batches.length, 0);
	timers.fire();
	assert.deepEqual(batches, [
		[{ type: "documents", id: 1 }, { type: "documents", id: 2 }, { type: "inbox" }],
	]);
});

test("changes after a hand-over open a new window", () => {
	const timers = manualTimers();
	const batches: LiveChange[][] = [];
	const coalescer = new ChangeCoalescer((changes) => batches.push(changes), 250, timers);
	coalescer.add({ type: "inbox" });
	timers.fire();
	coalescer.add({ type: "inbox" });
	timers.fire();
	assert.equal(batches.length, 2);
});

test("disposing drops what was not handed over", () => {
	const timers = manualTimers();
	const batches: LiveChange[][] = [];
	const coalescer = new ChangeCoalescer((changes) => batches.push(changes), 250, timers);
	coalescer.add({ type: "inbox" });
	coalescer.dispose();
	assert.equal(timers.pending(), 0);
	assert.deepEqual(batches, []);
});
