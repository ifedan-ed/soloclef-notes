import assert from "node:assert/strict";
import { test } from "node:test";
import {
	HEARTBEAT_TIMEOUT_MS,
	LiveChangesConnection,
} from "@/features/live-changes/lib/live-changes-connection";
import type { LiveChange } from "@/features/live-changes/model/live-change";

/** Let the connection's awaits run (reads, opens) before the test looks. */
const settle = async () => {
	for (let i = 0; i < 10; i++) await new Promise((resolve) => setImmediate(resolve));
};

/** A clock and timers the test moves by hand. */
function fakeClock() {
	let now = 0;
	let next = 1;
	const timers = new Map<number, { at: number; run: () => void }>();
	return {
		now: () => now,
		setTimer: (run: () => void, ms: number) => {
			const id = next++;
			timers.set(id, { at: now + ms, run });
			return id;
		},
		clearTimer: (id: unknown) => {
			timers.delete(id as number);
		},
		async advance(ms: number) {
			const until = now + ms;
			for (;;) {
				const dueTimers = [...timers.entries()]
					.filter(([, timer]) => timer.at <= until)
					.sort((a, b) => a[1].at - b[1].at);
				const first = dueTimers[0];
				if (!first) break;
				timers.delete(first[0]);
				now = first[1].at;
				first[1].run();
				await settle();
			}
			now = until;
			await settle();
		},
	};
}

/** One opened stream the test writes frames into, ended or cut at will. */
interface FakeStream {
	lastEventId: string | null;
	send: (text: string) => void;
	end: () => void;
	aborted: () => boolean;
}

/** The server: each open answers with the next scripted status (200 makes a stream). */
function fakeServer(statuses: Array<number | Error> = []) {
	const streams: FakeStream[] = [];
	const opens: Array<string | null> = [];
	const open = async (lastEventId: string | null, signal: AbortSignal) => {
		opens.push(lastEventId);
		const scripted = statuses.shift() ?? 200;
		if (scripted instanceof Error) throw scripted;
		if (scripted !== 200) return new Response(null, { status: scripted });
		let controller!: ReadableStreamDefaultController<Uint8Array>;
		let aborted = false;
		const body = new ReadableStream<Uint8Array>({
			start(c) {
				controller = c;
			},
		});
		signal.addEventListener("abort", () => {
			aborted = true;
			try {
				controller.error(new Error("aborted"));
			} catch {
				// already closed
			}
		});
		const encoder = new TextEncoder();
		streams.push({
			lastEventId,
			send: (text) => controller.enqueue(encoder.encode(text)),
			end: () => controller.close(),
			aborted: () => aborted,
		});
		return new Response(body, { status: 200, headers: { "Content-Type": "text/event-stream" } });
	};
	return { open, streams, opens };
}

function connect(server: ReturnType<typeof fakeServer>, clock = fakeClock()) {
	const readies: boolean[] = [];
	const changes: LiveChange[] = [];
	let signedOut = 0;
	const connection = new LiveChangesConnection({
		open: server.open,
		onReady: (resumed) => readies.push(resumed),
		onChange: (change) => changes.push(change),
		onSignedOut: () => {
			signedOut++;
		},
		now: clock.now,
		random: () => 0.5, // no jitter: the delays are the steps
		setTimer: clock.setTimer,
		clearTimer: clock.clearTimer,
	});
	let finished = false;
	void connection.start().then(() => {
		finished = true;
	});
	return {
		connection,
		clock,
		readies,
		changes,
		signedOut: () => signedOut,
		finished: () => finished,
	};
}

test("frames reach the app, and a reconnect resumes from the last change id", async () => {
	const server = fakeServer();
	const live = connect(server);
	await settle();
	assert.deepEqual(server.opens, [null]);

	const first = server.streams[0];
	first.send('data: {"type":"ready","resumed":false}\n\n');
	first.send('id: 1-0\ndata: {"type":"documents","id":12}\n\n: keepalive\n\n');
	first.send('id: 1-1\ndata: {"type":"outputs","notebook_id":7}\n\n');
	await settle();
	assert.deepEqual(live.readies, [false]);
	assert.deepEqual(live.changes, [
		{ type: "documents", id: 12 },
		{ type: "outputs", notebook_id: 7 },
	]);
	assert.equal(live.connection.resumeFrom, "1-1");

	first.end(); // the server went away
	await settle();
	assert.equal(server.opens.length, 1, "not at once: after the first delay");
	await live.clock.advance(1_000);
	assert.deepEqual(server.opens, [null, "1-1"]);

	server.streams[1].send('data: {"type":"ready","resumed":true}\n\n');
	await settle();
	assert.deepEqual(live.readies, [false, true]);
	live.connection.stop();
});

test("a 403 (API key, agent token) stops for good, without retrying", async () => {
	const server = fakeServer([403]);
	const live = connect(server);
	await settle();
	await live.clock.advance(120_000);
	assert.equal(server.opens.length, 1);
	assert.equal(live.finished(), true);
	assert.equal(live.signedOut(), 0);
});

test("a 401 after the session refresh means signed out: stop and say so", async () => {
	const server = fakeServer([401]);
	const live = connect(server);
	await settle();
	await live.clock.advance(120_000);
	assert.equal(server.opens.length, 1);
	assert.equal(live.signedOut(), 1);
	assert.equal(live.finished(), true);
});

test("failures back off 1 s, 2 s, 4 s; a long healthy connection starts over", async () => {
	const offline = new Error("offline");
	const server = fakeServer([offline, offline, offline]);
	const live = connect(server);
	await settle();
	assert.equal(server.opens.length, 1);
	await live.clock.advance(999);
	assert.equal(server.opens.length, 1);
	await live.clock.advance(1);
	assert.equal(server.opens.length, 2);
	await live.clock.advance(2_000);
	assert.equal(server.opens.length, 3);
	await live.clock.advance(3_999);
	assert.equal(server.opens.length, 3);
	await live.clock.advance(1);
	assert.equal(server.opens.length, 4, "the fourth open succeeds");

	// It stays up past the healthy mark (keepalives arriving), then drops.
	for (let i = 0; i < 2; i++) {
		server.streams[0].send(": keepalive\n\n");
		await live.clock.advance(25_000);
	}
	server.streams[0].end();
	await settle();
	await live.clock.advance(1_000);
	assert.equal(server.opens.length, 5, "back to the one-second step");
	live.connection.stop();
});

test("a silent connection is dropped after the heartbeat timeout and opened again", async () => {
	const server = fakeServer();
	const live = connect(server);
	await settle();
	server.streams[0].send('data: {"type":"ready","resumed":false}\n\n');
	await settle();
	// A keepalive keeps it alive past the first minute.
	await live.clock.advance(HEARTBEAT_TIMEOUT_MS - 10_000);
	server.streams[0].send(": keepalive\n\n");
	await settle();
	await live.clock.advance(HEARTBEAT_TIMEOUT_MS - 1);
	assert.equal(server.streams[0].aborted(), false);
	// Then nothing at all for a full timeout.
	await live.clock.advance(1);
	assert.equal(server.streams[0].aborted(), true);
	await live.clock.advance(1_000);
	assert.equal(server.opens.length, 2);
	live.connection.stop();
});

test("stop ends the connection and nothing opens again", async () => {
	const server = fakeServer();
	const live = connect(server);
	await settle();
	live.connection.stop();
	await settle();
	assert.equal(server.streams[0].aborted(), true);
	await live.clock.advance(120_000);
	assert.equal(server.opens.length, 1);
	assert.equal(live.finished(), true);
});
