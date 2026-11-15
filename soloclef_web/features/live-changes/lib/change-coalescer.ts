import type { LiveChange } from "../model/live-change";

/** How long changes gather before one refresh: indexing a folder sends many in a burst. */
export const COALESCE_WINDOW_MS = 250;

export interface CoalescerTimers {
	setTimer: (run: () => void, ms: number) => unknown;
	clearTimer: (handle: unknown) => void;
}

const browserTimers: CoalescerTimers = {
	setTimer: (run, ms) => setTimeout(run, ms),
	clearTimer: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
};

const signatureOf = (change: LiveChange) =>
	`${change.type}|${change.id ?? ""}|${change.notebook_id ?? ""}`;

/**
 * Gathers changes for a short window, then hands over each distinct one once.
 * The window opens with the first change and does not stretch with later
 * ones, so a steady stream of changes still refreshes every window.
 */
export class ChangeCoalescer {
	private pending = new Map<string, LiveChange>();
	private timer: unknown = null;

	constructor(
		private readonly flush: (changes: LiveChange[]) => void,
		private readonly windowMs: number = COALESCE_WINDOW_MS,
		private readonly timers: CoalescerTimers = browserTimers
	) {}

	add(change: LiveChange): void {
		this.pending.set(signatureOf(change), change);
		if (this.timer !== null) return;
		this.timer = this.timers.setTimer(() => {
			this.timer = null;
			const changes = [...this.pending.values()];
			this.pending.clear();
			this.flush(changes);
		}, this.windowMs);
	}

	/** Drops what has not been handed over yet (the stream stopped for good). */
	dispose(): void {
		if (this.timer !== null) this.timers.clearTimer(this.timer);
		this.timer = null;
		this.pending.clear();
	}
}
