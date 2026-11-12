import assert from "node:assert/strict";
import test from "node:test";
import { createSaveQueue, type NoteChanges, type SaveState } from "@/features/notes/lib/save-queue";

/** A queue whose timer is run by hand and whose sends answer when told. */
function harness() {
	const timers: (() => void)[] = [];
	const sent: NoteChanges[] = [];
	const answers: { resolve: (value: string) => void; reject: (error: Error) => void }[] = [];
	const states: SaveState[] = [];
	const saved: string[] = [];
	const queue = createSaveQueue<string>({
		delayMs: 1000,
		send: (changes) => {
			sent.push(changes);
			return new Promise((resolve, reject) => answers.push({ resolve, reject }));
		},
		onSaved: (value) => saved.push(value),
		onState: (state) => states.push(state),
		schedule: (run) => {
			timers.push(run);
			return timers.length - 1;
		},
		cancel: (handle) => {
			timers[handle as number] = () => undefined;
		},
	});
	const tick = () => timers.at(-1)?.();
	const settle = () => new Promise((resolve) => setImmediate(resolve));
	return { queue, sent, answers, states, saved, tick, settle };
}

test("keystrokes merge, and one save goes once typing pauses", async () => {
	const h = harness();
	h.queue.queue({ content: "a" });
	h.queue.queue({ content: "ab" });
	h.queue.queue({ title: "T" });
	assert.equal(h.sent.length, 0);
	assert.equal(h.states.at(-1), "unsaved");
	h.tick();
	await h.settle();
	assert.deepEqual(h.sent, [{ content: "ab", title: "T" }]);
	assert.equal(h.states.at(-1), "saving");
	h.answers[0].resolve("v1");
	await h.settle();
	assert.equal(h.states.at(-1), "saved");
	assert.deepEqual(h.saved, ["v1"]);
});

test("saves go one after another, and only the newest answer is kept", async () => {
	const h = harness();
	h.queue.queue({ content: "one" });
	void h.queue.flush();
	h.queue.queue({ content: "one two" });
	void h.queue.flush();
	await h.settle();
	// The second waits for the first.
	assert.equal(h.sent.length, 1);
	h.answers[0].resolve("first");
	await h.settle();
	assert.deepEqual(h.sent, [{ content: "one" }, { content: "one two" }]);
	assert.deepEqual(h.saved, [], "an answer older than a save on its way is not the note");
	h.answers[1].resolve("second");
	await h.settle();
	assert.deepEqual(h.saved, ["second"]);
});

test("a failed save keeps its words under anything typed since, and goes again", async () => {
	const h = harness();
	h.queue.queue({ title: "Old", content: "lost?" });
	void h.queue.flush();
	await h.settle();
	h.queue.queue({ content: "newer" });
	h.answers[0].reject(new Error("503"));
	await h.settle();
	assert.equal(h.states.at(-1), "failed");
	assert.equal(h.queue.hasUnsaved(), true);
	void h.queue.flush();
	await h.settle();
	assert.deepEqual(h.sent[1], { title: "Old", content: "newer" });
	h.answers[1].resolve("ok");
	await h.settle();
	assert.equal(h.states.at(-1), "saved");
	assert.equal(h.queue.hasUnsaved(), false);
});

test("flushing with nothing waiting sends nothing", async () => {
	const h = harness();
	await h.queue.flush();
	assert.equal(h.sent.length, 0);
});
