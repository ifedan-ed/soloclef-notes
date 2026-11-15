import assert from "node:assert/strict";
import test, { type TestContext } from "node:test";
import { randomClientId } from "@/features/presence/lib/presence-client-id";
import {
	PRESENCE_BEAT_MS,
	type PresenceDocument,
	type PresenceWindow,
	startPresenceHeartbeat,
} from "@/features/presence/lib/presence-heartbeat";

/**
 * The tab tells the server it is on screen (no phone push meanwhile): at
 * once when visible, every minute while visible, "gone" when hidden or left,
 * and nothing at all while hidden.
 */

class FakeDocument extends EventTarget implements PresenceDocument {
	visibilityState: DocumentVisibilityState;
	constructor(state: DocumentVisibilityState) {
		super();
		this.visibilityState = state;
	}
	turn(state: DocumentVisibilityState) {
		this.visibilityState = state;
		this.dispatchEvent(new Event("visibilitychange"));
	}
}

class FakeWindow extends EventTarget implements PresenceWindow {
	fire(type: "pagehide" | "pageshow") {
		this.dispatchEvent(new Event(type));
	}
}

function setup(t: TestContext, state: DocumentVisibilityState) {
	t.mock.timers.enable({ apis: ["setInterval"] });
	const doc = new FakeDocument(state);
	const win = new FakeWindow();
	const sent: boolean[] = [];
	const stop = startPresenceHeartbeat({ doc, win, send: (active) => sent.push(active) });
	return { doc, win, sent, stop, tick: (ms: number) => t.mock.timers.tick(ms) };
}

test("a page that opens visible says so at once, then every minute", (t) => {
	const { sent, tick } = setup(t, "visible");
	assert.deepEqual(sent, [true]);
	tick(PRESENCE_BEAT_MS - 1);
	assert.deepEqual(sent, [true]);
	tick(1);
	assert.deepEqual(sent, [true, true]);
	tick(PRESENCE_BEAT_MS * 2);
	assert.deepEqual(sent, [true, true, true, true]);
});

test("the beat stays well inside the server's 150 s mark", () => {
	assert.ok(PRESENCE_BEAT_MS * 2 < 150_000);
});

test("a page that opens hidden sends nothing until it is shown", (t) => {
	const { doc, sent, tick } = setup(t, "hidden");
	tick(PRESENCE_BEAT_MS * 3);
	assert.deepEqual(sent, []);
	doc.turn("visible");
	assert.deepEqual(sent, [true]);
});

test("hiding says gone once and stops the beat; showing again starts it", (t) => {
	const { doc, sent, tick } = setup(t, "visible");
	doc.turn("hidden");
	assert.deepEqual(sent, [true, false]);
	tick(PRESENCE_BEAT_MS * 5);
	assert.deepEqual(sent, [true, false]);
	doc.turn("visible");
	tick(PRESENCE_BEAT_MS);
	assert.deepEqual(sent, [true, false, true, true]);
});

test("leaving the page says gone, once, however many events say so", (t) => {
	const { doc, win, sent, tick } = setup(t, "visible");
	win.fire("pagehide");
	doc.turn("hidden");
	tick(PRESENCE_BEAT_MS * 2);
	assert.deepEqual(sent, [true, false]);
});

test("a page brought back from the back-forward cache beats again", (t) => {
	const { win, sent } = setup(t, "visible");
	win.fire("pagehide");
	win.fire("pageshow");
	assert.deepEqual(sent, [true, false, true]);
});

test("stopping (leaving the signed-in pages) says gone and then nothing more", (t) => {
	const { doc, sent, stop, tick } = setup(t, "visible");
	stop();
	assert.deepEqual(sent, [true, false]);
	doc.turn("visible");
	tick(PRESENCE_BEAT_MS * 2);
	assert.deepEqual(sent, [true, false]);
});

test("stopping a hidden page says nothing", (t) => {
	const { sent, stop } = setup(t, "hidden");
	stop();
	assert.deepEqual(sent, []);
});

test("a tab's id fits the server's 8 to 64 characters of [A-Za-z0-9_-], and two tabs differ", () => {
	const id = randomClientId();
	assert.match(id, /^[A-Za-z0-9_-]{8,64}$/);
	assert.notEqual(randomClientId(), id);
});
