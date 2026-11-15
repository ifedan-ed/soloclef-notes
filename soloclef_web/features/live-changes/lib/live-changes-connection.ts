/**
 * One tab's connection to the change stream (`GET /api/v1/events`): opens it,
 * reads its frames, notices when it has gone quiet, and opens it again with
 * the last change id it saw so the server can replay what was missed.
 *
 * Everything outside the loop (the request, the clock, the timers, chance) is
 * passed in, so the reconnect and resume behaviour runs in tests unchanged.
 */
import { type LiveChange, parseStreamMessage } from "../model/live-change";
import { attemptAfterConnection, reconnectDelayMs } from "./reconnect-backoff";
import { type SseFrame, SseFrameParser } from "./sse-frame-parser";

/** The server writes a keepalive every 25 s; this long without a byte means the line is dead. */
export const HEARTBEAT_TIMEOUT_MS = 60_000;

export interface LiveChangesConnectionDeps {
	/**
	 * Opens the stream. `lastEventId` is the id of the last change frame seen
	 * on an earlier connection (null on the first). Aborting `signal` must end
	 * the request and its body.
	 */
	open: (lastEventId: string | null, signal: AbortSignal) => Promise<Response>;
	/** The connection's first frame: `resumed` false means changes may have been missed. */
	onReady: (resumed: boolean) => void;
	onChange: (change: LiveChange) => void;
	/** The server answered 401 after the session refresh: the person is signed out. */
	onSignedOut: () => void;
	now?: () => number;
	random?: () => number;
	setTimer?: (run: () => void, ms: number) => unknown;
	clearTimer?: (handle: unknown) => void;
	heartbeatTimeoutMs?: number;
}

type Outcome = "reconnect" | "stop";

export class LiveChangesConnection {
	private started = false;
	private stopped = false;
	private attempt = 0;
	private lastEventId: string | null = null;
	private controller: AbortController | null = null;
	private wakeFromWait: (() => void) | null = null;
	private readonly now: () => number;
	private readonly random: () => number;
	private readonly setTimer: (run: () => void, ms: number) => unknown;
	private readonly clearTimer: (handle: unknown) => void;
	private readonly heartbeatTimeoutMs: number;

	constructor(private readonly deps: LiveChangesConnectionDeps) {
		this.now = deps.now ?? Date.now;
		this.random = deps.random ?? Math.random;
		this.setTimer = deps.setTimer ?? ((run, ms) => setTimeout(run, ms));
		this.clearTimer =
			deps.clearTimer ?? ((handle) => clearTimeout(handle as ReturnType<typeof setTimeout>));
		this.heartbeatTimeoutMs = deps.heartbeatTimeoutMs ?? HEARTBEAT_TIMEOUT_MS;
	}

	/** The id a reconnect would resume from (for tests and diagnostics). */
	get resumeFrom(): string | null {
		return this.lastEventId;
	}

	/** Resolves when the connection has stopped for good (stop(), 401 or 403). */
	start(): Promise<void> {
		if (this.started) throw new Error("LiveChangesConnection.start called twice");
		this.started = true;
		return this.loop();
	}

	/** Close the stream and never open it again (sign-out, page left). */
	stop(): void {
		this.stopped = true;
		this.controller?.abort();
		this.wakeFromWait?.();
	}

	private async loop(): Promise<void> {
		while (!this.stopped) {
			const outcome = await this.connectOnce();
			if (this.stopped || outcome === "stop") return;
			const delay = reconnectDelayMs(this.attempt, this.random());
			this.attempt += 1;
			await this.wait(delay);
		}
	}

	private async connectOnce(): Promise<Outcome> {
		const controller = new AbortController();
		this.controller = controller;
		let response: Response;
		try {
			response = await this.deps.open(this.lastEventId, controller.signal);
		} catch {
			return "reconnect"; // offline, DNS, aborted: try again after the delay
		}
		if (response.status === 401) {
			this.stopped = true;
			this.deps.onSignedOut();
			return "stop";
		}
		if (response.status === 403) {
			// An API key or agent token: the stream is for signed-in sessions,
			// and asking again would be refused the same way.
			this.stopped = true;
			return "stop";
		}
		if (!response.ok || !response.body) {
			void response.body?.cancel().catch(() => undefined);
			return "reconnect";
		}
		const openedAt = this.now();
		await this.read(response.body, controller);
		this.attempt = attemptAfterConnection(this.attempt, this.now() - openedAt);
		return "reconnect";
	}

	/** Reads frames until the body ends, errors, or goes quiet past the heartbeat timeout. */
	private async read(body: ReadableStream<Uint8Array>, controller: AbortController): Promise<void> {
		const reader = body.getReader();
		const decoder = new TextDecoder();
		const parser = new SseFrameParser();
		let watchdog: unknown = null;
		const drop = () => {
			controller.abort();
			void reader.cancel().catch(() => undefined);
		};
		const armWatchdog = () => {
			if (watchdog !== null) this.clearTimer(watchdog);
			watchdog = this.setTimer(drop, this.heartbeatTimeoutMs);
		};
		armWatchdog();
		try {
			for (;;) {
				const { done, value } = await reader.read();
				if (done || this.stopped) return;
				// Any bytes are life: the keepalive comment included.
				armWatchdog();
				for (const frame of parser.push(decoder.decode(value, { stream: true }))) {
					this.handle(frame);
				}
			}
		} catch {
			// The body errored or was aborted (watchdog, stop, restart): reconnect decides.
		} finally {
			if (watchdog !== null) this.clearTimer(watchdog);
			void reader.cancel().catch(() => undefined);
		}
	}

	private handle(frame: SseFrame): void {
		if (frame.id !== undefined) this.lastEventId = frame.id;
		const message = parseStreamMessage(frame.data);
		if (message.kind === "ready") this.deps.onReady(message.resumed);
		else if (message.kind === "change") this.deps.onChange(message.change);
	}

	private wait(ms: number): Promise<void> {
		return new Promise((resolve) => {
			const timer = this.setTimer(() => {
				this.wakeFromWait = null;
				resolve();
			}, ms);
			this.wakeFromWait = () => {
				this.clearTimer(timer);
				this.wakeFromWait = null;
				resolve();
			};
		});
	}
}
