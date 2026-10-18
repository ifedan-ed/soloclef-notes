import assert from "node:assert/strict";
import test from "node:test";

import { ServerTurnWatcher } from "@/features/chat-thread/server-turn/server-turn-watcher";
import {
	parseServerTurnStatus,
	type ServerTurnStatus,
} from "@/features/chat-thread/server-turn/turn-status-api";
import {
	askForTurnStatusCheck,
	onTurnStatusCheckRequested,
} from "@/features/chat-thread/server-turn/turn-status-signal";

/** A watcher wired to scripted backend replies and a hand-cranked timer. */
function harness(replies: Array<ServerTurnStatus | Error>, { hidden = false } = {}) {
	const statuses: ServerTurnStatus[] = [];
	const stopRequests: boolean[] = [];
	let finished = 0;
	let fetches = 0;
	const timers: Array<() => void> = [];
	const watcher = new ServerTurnWatcher(
		117,
		{
			fetchStatus: async (threadId) => {
				assert.equal(threadId, 117);
				fetches += 1;
				const reply = replies.shift() ?? "idle";
				if (reply instanceof Error) throw reply;
				return reply;
			},
			onStatus: (status) => statuses.push(status),
			onFinished: () => {
				finished += 1;
			},
			onStopRequested: (requested) => stopRequests.push(requested),
			isHidden: () => hidden,
			setTimer: (run) => {
				timers.push(run);
				return timers.length;
			},
			clearTimer: () => undefined,
		},
		4_000
	);
	return {
		watcher,
		statuses,
		stopRequests,
		timers,
		/** What the page shows: "Stopping…" instead of Stop. */
		get stopping() {
			return stopRequests.at(-1) === true || statuses.at(-1) === "cancelling";
		},
		get finished() {
			return finished;
		},
		get fetches() {
			return fetches;
		},
		async tick() {
			const run = timers.shift();
			assert.ok(run, "a poll was scheduled");
			run();
			await new Promise((resolve) => setImmediate(resolve));
		},
	};
}

test("a turn started elsewhere shows as running until the backend says idle, then reloads", async () => {
	const h = harness(["busy", "busy", "idle"]);
	await h.watcher.check();
	assert.deepEqual(h.statuses, ["busy"]);
	assert.equal(h.timers.length, 1, "keeps checking while it runs");

	await h.tick();
	assert.deepEqual(h.statuses, ["busy"]);
	await h.tick();
	assert.deepEqual(h.statuses, ["busy", "idle"]);
	assert.equal(h.finished, 1, "the finished answer is loaded once");
	assert.equal(h.timers.length, 0, "stops checking when idle");
});

test("an idle thread is checked once and not polled", async () => {
	const h = harness(["idle"]);
	await h.watcher.check();
	assert.deepEqual(h.statuses, []);
	assert.equal(h.timers.length, 0);
	assert.equal(h.finished, 0);
});

test("the page that streams the turn does not poll; letting go checks again", async () => {
	const h = harness(["idle"]);
	h.watcher.pause();
	await h.watcher.check();
	assert.equal(h.fetches, 0);
	h.watcher.resume();
	await new Promise((resolve) => setImmediate(resolve));
	assert.equal(h.fetches, 1);
});

test("a failed check keeps the running state and tries again", async () => {
	const h = harness(["busy", new Error("offline"), "idle"]);
	await h.watcher.check();
	await h.tick();
	assert.deepEqual(h.statuses, ["busy"], "a network error is not 'finished'");
	assert.equal(h.timers.length, 1);
	await h.tick();
	assert.deepEqual(h.statuses, ["busy", "idle"]);
});

test("a hidden tab does not poll; it checks again when shown", async () => {
	const h = harness(["busy"], { hidden: true });
	await h.watcher.check();
	assert.deepEqual(h.statuses, ["busy"]);
	assert.equal(h.timers.length, 0);
});

test("stopping shows as running (cancelling) until the partial reply is saved", async () => {
	const h = harness(["cancelling", "idle"]);
	await h.watcher.check();
	assert.deepEqual(h.statuses, ["cancelling"]);
	await h.tick();
	assert.deepEqual(h.statuses, ["cancelling", "idle"]);
	assert.equal(h.finished, 1);
});

test("a 409 or an answered Stop reaches the page showing that thread", () => {
	const seen: number[] = [];
	const unsubscribe = onTurnStatusCheckRequested((threadId) => seen.push(threadId));
	askForTurnStatusCheck(117);
	unsubscribe();
	askForTurnStatusCheck(118);
	assert.deepEqual(seen, [117]);
});

test("after a hint the follow-up check comes quickly, then normal polling resumes", async () => {
	const delays: number[] = [];
	const replies: ServerTurnStatus[] = ["busy", "busy"];
	const pending: Array<() => void> = [];
	const watcher = new ServerTurnWatcher(
		117,
		{
			fetchStatus: async () => replies.shift() ?? "idle",
			onStatus: () => undefined,
			onFinished: () => undefined,
			setTimer: (run, ms) => {
				delays.push(ms);
				pending.push(run);
				return pending.length;
			},
			clearTimer: () => undefined,
		},
		4_000,
		1_000
	);
	await watcher.check({ soon: true });
	assert.deepEqual(delays, [1_000]);
	pending.shift()?.();
	await new Promise((resolve) => setImmediate(resolve));
	assert.deepEqual(delays, [1_000, 4_000]);
});

test("turn-status replies are read strictly", () => {
	assert.equal(parseServerTurnStatus({ status: "busy" }), "busy");
	assert.equal(parseServerTurnStatus({ status: "cancelling" }), "cancelling");
	assert.equal(parseServerTurnStatus({ status: "weird" }), null);
	assert.equal(parseServerTurnStatus(null), null);
});

/** A promise the test settles by hand. */
function deferred<T = void>() {
	let resolve!: (value: T) => void;
	let reject!: (error: unknown) => void;
	const promise = new Promise<T>((res, rej) => {
		resolve = res;
		reject = rej;
	});
	return { promise, resolve, reject };
}

const settle = () => new Promise((resolve) => setImmediate(resolve));

test("after Stop the page shows Stopping…, never Stop, until the turn has saved", async () => {
	// busy, then a poll that lands before the Stop request does (still busy),
	// then the backend's answer (cancelling while it saves), then idle.
	const h = harness(["busy", "busy", "cancelling", "idle"]);
	await h.watcher.check();
	assert.equal(h.stopping, false);

	const answer = deferred();
	const stopped = h.watcher.stop(() => answer.promise);
	assert.equal(h.stopping, true, "Stopping… from the press");

	await h.tick();
	assert.deepEqual(h.statuses, ["busy"]);
	assert.equal(h.stopping, true, "a busy poll before the answer does not bring Stop back");

	answer.resolve();
	await stopped;
	assert.deepEqual(h.statuses, ["busy", "cancelling"]);
	assert.deepEqual(h.stopRequests, [true, false]);
	assert.equal(h.stopping, true, "the backend now says cancelling");

	await h.tick();
	assert.deepEqual(h.statuses, ["busy", "cancelling", "idle"]);
	assert.equal(h.stopping, false);
	assert.equal(h.finished, 1, "the saved partial reply is loaded");
});

test("Stop pressed again while the first is answered sends nothing", async () => {
	const h = harness(["busy", "idle"]);
	await h.watcher.check();
	let sent = 0;
	const answer = deferred();
	const send = () => {
		sent += 1;
		return answer.promise;
	};
	const first = h.watcher.stop(send);
	const second = h.watcher.stop(send);
	await second;
	assert.equal(sent, 1);
	answer.resolve();
	await first;
	assert.equal(sent, 1);
	assert.deepEqual(h.stopRequests, [true, false]);
});

test("a Stop request that fails shows Stop again", async () => {
	const h = harness(["busy", "busy"]);
	await h.watcher.check();
	await assert.rejects(
		h.watcher.stop(async () => {
			throw new Error("cancel-active-turn 500");
		})
	);
	assert.deepEqual(h.stopRequests, [true, false]);
	assert.equal(h.stopping, false);
	assert.deepEqual(h.statuses, ["busy"]);
});

test("refresh waits for a status fetched after it was asked for", async () => {
	const fetches: Array<ReturnType<typeof deferred<ServerTurnStatus>>> = [];
	const statuses: ServerTurnStatus[] = [];
	const watcher = new ServerTurnWatcher(
		117,
		{
			fetchStatus: () => {
				const reply = deferred<ServerTurnStatus>();
				fetches.push(reply);
				return reply.promise;
			},
			onStatus: (status) => statuses.push(status),
			onFinished: () => undefined,
			setTimer: () => 0,
			clearTimer: () => undefined,
		},
		4_000
	);
	void watcher.check(); // a check already on its way, asked before the Stop
	let refreshed = false;
	const refresh = watcher.refresh().then(() => {
		refreshed = true;
	});
	fetches[0].resolve("busy");
	await settle();
	assert.equal(refreshed, false, "the older answer does not count");
	assert.equal(fetches.length, 2, "it asked again");
	fetches[1].resolve("cancelling");
	await refresh;
	assert.deepEqual(statuses, ["busy", "cancelling"]);
});

test("refresh never hangs on a paused or disposed watcher", async () => {
	const h = harness(["busy"]);
	h.watcher.pause();
	await h.watcher.refresh();
	h.watcher.dispose();
	await h.watcher.refresh();
	assert.equal(h.fetches, 0);
});
