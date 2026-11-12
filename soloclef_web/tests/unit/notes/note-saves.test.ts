import assert from "node:assert/strict";
import test from "node:test";
import { createNoteSaves } from "@/features/notes/lib/note-saves";
import type { NoteChanges, SaveState } from "@/features/notes/lib/save-queue";

/** Note saves whose timer is run by hand and whose sends answer when told. */
function harness() {
	const timers: (() => void)[] = [];
	const sent: { noteId: number; changes: NoteChanges }[] = [];
	const answers: { resolve: (value: string) => void; reject: (error: Error) => void }[] = [];
	const typed: { noteId: number; changes: NoteChanges }[] = [];
	const saved: { noteId: number; value: string }[] = [];
	const saves = createNoteSaves<string>({
		delayMs: 1000,
		send: (noteId, changes) => {
			sent.push({ noteId, changes });
			return new Promise((resolve, reject) => answers.push({ resolve, reject }));
		},
		onTyped: (noteId, changes) => typed.push({ noteId, changes }),
		onSaved: (noteId, value) => saved.push({ noteId, value }),
		schedule: (run) => {
			timers.push(run);
			return timers.length - 1;
		},
		cancel: (handle) => {
			timers[handle as number] = () => undefined;
		},
	});
	const settle = () => new Promise((resolve) => setImmediate(resolve));
	return { saves, sent, answers, typed, saved, settle };
}

test("what is typed is reported at once, before any save", () => {
	const h = harness();
	const note = h.saves.open(7, () => undefined);
	note.queue({ content: "First line. alpha" });
	assert.deepEqual(h.typed, [{ noteId: 7, changes: { content: "First line. alpha" } }]);
	assert.equal(h.sent.length, 0);
});

test("a note opened again while its save is in flight keeps one queue: the next save waits its turn", async () => {
	const h = harness();
	const first = h.saves.open(7, () => undefined);
	first.queue({ content: "First line. alpha" });
	// Left: what is waiting is sent, and is still on its way.
	first.close();
	await h.settle();
	assert.deepEqual(h.sent, [{ noteId: 7, changes: { content: "First line. alpha" } }]);

	// Opened again, typed in, left again before the first save is answered.
	const states: SaveState[] = [];
	const second = h.saves.open(7, (state) => states.push(state));
	assert.equal(states.at(-1), "saving", "the new opening is told a save is still going");
	second.queue({ content: "First line. alpha beta" });
	second.close();
	await h.settle();
	// One queue: the second save is not sent beside the first.
	assert.equal(h.sent.length, 1);
	assert.equal(h.saves.size(), 1, "closed but unsaved, the queue is kept");

	h.answers[0].resolve("server: alpha");
	await h.settle();
	assert.deepEqual(h.sent[1], { noteId: 7, changes: { content: "First line. alpha beta" } });
	// The older answer is not "the note as saved": newer words are in flight.
	assert.deepEqual(h.saved, []);
	h.answers[1].resolve("server: alpha beta");
	await h.settle();
	assert.deepEqual(h.saved, [{ noteId: 7, value: "server: alpha beta" }]);
	assert.equal(h.saves.size(), 0, "closed and saved, the queue is forgotten");
});

test("a save that failed while the note was closed is sent again when it is opened", async () => {
	const h = harness();
	const first = h.saves.open(7, () => undefined);
	first.queue({ content: "kept" });
	first.close();
	await h.settle();
	h.answers[0].reject(new Error("offline"));
	await h.settle();
	assert.equal(h.saves.size(), 1, "the words are not forgotten");

	const states: SaveState[] = [];
	const second = h.saves.open(7, (state) => states.push(state));
	assert.equal(states[0], "failed");
	await h.settle();
	assert.deepEqual(h.sent[1], { noteId: 7, changes: { content: "kept" } });
	h.answers[1].resolve("server: kept");
	await h.settle();
	assert.deepEqual(h.saved, [{ noteId: 7, value: "server: kept" }]);
	assert.equal(states.at(-1), "saved");
	second.close();
	await h.settle();
	assert.equal(h.saves.size(), 0);
});

test("two notes save on their own, and a closed opening hears nothing more", async () => {
	const h = harness();
	const heard: SaveState[] = [];
	const one = h.saves.open(1, (state) => heard.push(state));
	const two = h.saves.open(2, () => undefined);
	one.queue({ title: "One" });
	two.queue({ title: "Two" });
	one.close();
	const before = heard.length;
	await h.settle();
	h.answers[0].resolve("server: one");
	await h.settle();
	assert.equal(heard.length, before, "nothing is reported to an opening that closed");
	assert.deepEqual(
		h.sent.map((s) => s.noteId),
		[1]
	);
	assert.equal(h.saves.size(), 1, "note 1 is done; note 2 is open");
	// Note 2's save goes on its own queue, and is answered on its own.
	const flushed = two.flush();
	await h.settle();
	assert.deepEqual(h.sent[1], { noteId: 2, changes: { title: "Two" } });
	h.answers[1].resolve("server: two");
	await flushed;
	assert.deepEqual(h.saved.at(-1), { noteId: 2, value: "server: two" });
});
