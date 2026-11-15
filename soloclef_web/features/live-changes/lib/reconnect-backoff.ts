/**
 * When to open the change stream again after it drops (the contract's client
 * rules): 1 s, 2 s, 4 s ... capped at 30 s, each spread by jitter so the tabs
 * of a server that just restarted do not all come back in the same instant.
 */

export const FIRST_RECONNECT_DELAY_MS = 1_000;
export const MAX_RECONNECT_DELAY_MS = 30_000;
/** A connection that lived this long was healthy: the next drop starts the delays over. */
export const HEALTHY_CONNECTION_MS = 30_000;
/** Each delay lands anywhere from 75 % to 125 % of its step. */
const JITTER_SPREAD = 0.5;

/**
 * The wait before reconnect number `attempt` (0 for the first after a drop).
 * `random` is a number in [0, 1), Math.random in the app and fixed in tests.
 */
export function reconnectDelayMs(attempt: number, random: number): number {
	const step = Math.min(
		FIRST_RECONNECT_DELAY_MS * 2 ** Math.max(0, attempt),
		MAX_RECONNECT_DELAY_MS
	);
	const jittered = step * (1 - JITTER_SPREAD / 2 + JITTER_SPREAD * random);
	return Math.round(Math.min(jittered, MAX_RECONNECT_DELAY_MS));
}

/** The attempt count after a connection that stayed open `livedMs`. */
export function attemptAfterConnection(attempt: number, livedMs: number): number {
	return livedMs > HEALTHY_CONNECTION_MS ? 0 : attempt;
}
