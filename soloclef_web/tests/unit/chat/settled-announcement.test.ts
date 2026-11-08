import assert from "node:assert/strict";
import test, { type TestContext } from "node:test";

import {
	STEP_ANNOUNCE_SETTLE_MS,
	settledAnnouncer,
} from "@/features/chat-messages/timeline/settled-announcement";

/**
 * A screen reader hears an answer's one line once it settles on a step, not
 * each change (the owner's email answer flips "Searching email" / "Searched
 * email" eight times over), and never the same step twice in a row.
 */

function setup(t: TestContext) {
	t.mock.timers.enable({ apis: ["setTimeout"] });
	const said: string[] = [];
	const announcer = settledAnnouncer((text) => said.push(text));
	return { said, announcer, tick: (ms: number) => t.mock.timers.tick(ms) };
}

const EMAIL_BURST = [
	"Listing email accounts",
	"Listed email accounts",
	"Searching email",
	"Searched email",
	"Searching email",
	"Searched email",
	"Searching email",
];

test("a burst of steps is said once, as the step the line settles on", (t) => {
	const { said, announcer, tick } = setup(t);
	for (const label of EMAIL_BURST) {
		announcer.update(label);
		tick(200);
	}
	assert.deepEqual(said, []);
	tick(STEP_ANNOUNCE_SETTLE_MS);
	assert.deepEqual(said, ["Searching email"]);
	tick(60_000);
	assert.deepEqual(said, ["Searching email"]);
});

test("each change starts the wait again; a step held long enough is said", (t) => {
	const { said, announcer, tick } = setup(t);
	announcer.update("Reasoning");
	tick(STEP_ANNOUNCE_SETTLE_MS - 1);
	announcer.update("Listing email accounts");
	tick(STEP_ANNOUNCE_SETTLE_MS - 1);
	assert.deepEqual(said, []);
	tick(1);
	assert.deepEqual(said, ["Listing email accounts"]);
	announcer.update("Searching email");
	tick(STEP_ANNOUNCE_SETTLE_MS);
	assert.deepEqual(said, ["Listing email accounts", "Searching email"]);
});

test("the same step again is not said twice, even after a flip away and back", (t) => {
	const { said, announcer, tick } = setup(t);
	announcer.update("Searching email");
	tick(STEP_ANNOUNCE_SETTLE_MS);
	announcer.update("Searched email");
	tick(300);
	announcer.update("Searching email");
	tick(STEP_ANNOUNCE_SETTLE_MS * 2);
	assert.deepEqual(said, ["Searching email"]);
});

test("a render with the same step does not restart its wait", (t) => {
	const { said, announcer, tick } = setup(t);
	announcer.update("Reasoning");
	tick(1000);
	announcer.update("Reasoning");
	tick(STEP_ANNOUNCE_SETTLE_MS - 1000);
	assert.deepEqual(said, ["Reasoning"]);
});

test("nothing waiting is said once the answer stops working or the line goes", (t) => {
	const { said, announcer, tick } = setup(t);
	announcer.update("Searching email");
	tick(500);
	announcer.update("");
	tick(STEP_ANNOUNCE_SETTLE_MS * 2);
	assert.deepEqual(said, []);
	announcer.update("Searched email");
	tick(500);
	announcer.dispose();
	tick(STEP_ANNOUNCE_SETTLE_MS * 2);
	assert.deepEqual(said, []);
	// Set up again after a dispose (React's strict mode runs effects twice): the step waits afresh.
	announcer.update("Searched email");
	tick(STEP_ANNOUNCE_SETTLE_MS);
	assert.deepEqual(said, ["Searched email"]);
});
