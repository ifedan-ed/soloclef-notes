import type { ServerTurnStatus } from "./turn-status-api";

/**
 * While another page's turn runs on this thread, how often this page asks the
 * backend whether it is still running. A Zero change, the tab becoming visible
 * or a 409 from the backend also triggers an immediate check. Hidden tabs do
 * not poll. Documented in custom/docs/chat-turns.md.
 */
export const SERVER_TURN_POLL_MS = 4_000;

/**
 * After a hint that the turn just changed (Zero's flag flipped, a 409, a Stop
 * was answered), the follow-up check comes this soon instead: the backend
 * keeps the thread busy until the answer is saved, a moment after the hint.
 */
export const SERVER_TURN_SETTLE_MS = 1_000;

export interface ServerTurnWatcherDeps {
	fetchStatus: (threadId: number) => Promise<ServerTurnStatus>;
	/** The status this page should show changed. */
	onStatus: (status: ServerTurnStatus) => void;
	/** A turn this page saw running has ended: reload the thread's messages. */
	onFinished: () => void;
	/**
	 * Stop was pressed on this page and its answer has not been checked yet:
	 * the page shows "Stopping…" meanwhile, not Stop.
	 */
	onStopRequested?: (requested: boolean) => void;
	isHidden?: () => boolean;
	setTimer?: (run: () => void, ms: number) => unknown;
	clearTimer?: (handle: unknown) => void;
}

/**
 * Tracks whether a turn is running on a thread when this page does not hold
 * its live stream (another tab or device started it, or this page was
 * reloaded). The page then shows the turn as running, with Stop instead of
 * Regenerate, until the backend says it is idle.
 */
export class ServerTurnWatcher {
	private status: ServerTurnStatus = "idle";
	private inFlight = false;
	private checkAgain = false;
	private settling = false;
	private paused = false;
	private disposed = false;
	private stopRequested = false;
	private timer: unknown = null;
	/** ``refresh()`` callers waiting for a status fetched after they asked. */
	private waiters: Array<() => void> = [];
	private readonly setTimer: (run: () => void, ms: number) => unknown;
	private readonly clearTimer: (handle: unknown) => void;
	private readonly isHidden: () => boolean;

	constructor(
		private readonly threadId: number,
		private readonly deps: ServerTurnWatcherDeps,
		private readonly pollMs: number = SERVER_TURN_POLL_MS,
		private readonly settleMs: number = SERVER_TURN_SETTLE_MS
	) {
		this.setTimer = deps.setTimer ?? ((run, ms) => setTimeout(run, ms));
		this.clearTimer =
			deps.clearTimer ?? ((handle) => clearTimeout(handle as ReturnType<typeof setTimeout>));
		this.isHidden = deps.isHidden ?? (() => false);
	}

	get current(): ServerTurnStatus {
		return this.status;
	}

	/**
	 * This page now holds the turn's live stream (it sent the message), which
	 * shows the turn itself: stop checking until it lets go.
	 */
	pause(): void {
		this.paused = true;
		this.cancelTimer();
		this.releaseWaiters();
		this.setStatus("idle", { finished: false });
	}

	resume(): void {
		if (!this.paused) return;
		this.paused = false;
		void this.check();
	}

	/** Ask the backend now; ``soon`` makes the follow-up check come quickly. */
	async check({ soon = false }: { soon?: boolean } = {}): Promise<void> {
		if (this.disposed || this.paused) {
			this.releaseWaiters();
			return;
		}
		if (soon) this.settling = true;
		if (this.inFlight) {
			this.checkAgain = true;
			return;
		}
		this.cancelTimer();
		this.inFlight = true;
		let next: ServerTurnStatus | null = null;
		try {
			next = await this.deps.fetchStatus(this.threadId);
		} catch {
			next = null; // keep what we knew; try again on the next tick
		} finally {
			this.inFlight = false;
		}
		if (this.disposed || this.paused) {
			this.releaseWaiters();
			return;
		}
		if (next) this.setStatus(next, { finished: true });
		if (this.checkAgain) {
			this.checkAgain = false;
			void this.check();
			return;
		}
		this.releaseWaiters();
		const delay = this.settling ? Math.min(this.settleMs, this.pollMs) : this.pollMs;
		this.settling = false;
		if (this.status !== "idle" && !this.isHidden()) {
			this.timer = this.setTimer(() => {
				this.timer = null;
				void this.check();
			}, delay);
		}
	}

	/**
	 * Ask the backend now and resolve once a status fetched after this call
	 * has been applied (at once when the watcher is paused or disposed; a
	 * failed fetch keeps the last status).
	 */
	refresh(): Promise<void> {
		if (this.disposed || this.paused) return Promise.resolve();
		return new Promise((resolve) => {
			this.waiters.push(resolve);
			void this.check({ soon: true });
		});
	}

	/**
	 * Stop the turn: ``send`` asks the backend (the request returns once the
	 * turn has saved, or after the backend's wait). The page shows the turn as
	 * stopping from the press until a status fetched after the answer is in,
	 * so Stop is never offered again in between. A press while one is still
	 * being answered sends nothing. A failed ``send`` rejects.
	 */
	async stop(send: () => Promise<void>): Promise<void> {
		if (this.stopRequested) return;
		this.setStopRequested(true);
		try {
			await send();
		} finally {
			await this.refresh();
			this.setStopRequested(false);
		}
	}

	dispose(): void {
		this.disposed = true;
		this.cancelTimer();
		this.releaseWaiters();
	}

	private setStopRequested(requested: boolean): void {
		this.stopRequested = requested;
		if (!this.disposed) this.deps.onStopRequested?.(requested);
	}

	private releaseWaiters(): void {
		const waiters = this.waiters;
		this.waiters = [];
		for (const resolve of waiters) resolve();
	}

	private setStatus(next: ServerTurnStatus, { finished }: { finished: boolean }): void {
		const previous = this.status;
		if (previous === next) return;
		this.status = next;
		this.deps.onStatus(next);
		if (finished && previous !== "idle" && next === "idle") {
			this.deps.onFinished();
		}
	}

	private cancelTimer(): void {
		if (this.timer !== null) {
			this.clearTimer(this.timer);
			this.timer = null;
		}
	}
}
