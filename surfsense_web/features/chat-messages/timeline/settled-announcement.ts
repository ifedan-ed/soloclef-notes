/**
 * What a screen reader is told about an answer's one line while the answer
 * works (2026-10-10). The line's step can change several times a second
 * ("Searching email" / "Searched email", eight times over, in the owner's
 * "ok mail from protective?" answer); saying each change would talk over the
 * person. So a step is said only once the line has stayed on it for
 * STEP_ANNOUNCE_SETTLE_MS (each change starts the wait again), the same step
 * is not said twice in a row, and the clock is never said.
 *
 * Pure (no React), so the phone app's copy (soloclef_mobile
 * src/lib/chat/settledAnnouncer.ts) is held to it by its web-parity test.
 * The web says it through a polite status region next to the line
 * (use-settled-announcement.ts, interleaved-trace.tsx); the app through
 * AccessibilityInfo.announceForAccessibility.
 */

/** How long the line must stay on a step before that step is said. */
export const STEP_ANNOUNCE_SETTLE_MS = 1500;

export interface SettledAnnouncer {
	/** The line now names `text` ("" when nothing is to be said: the answer is not working). */
	update(text: string): void;
	/** Stops: a step still waiting is not said. */
	dispose(): void;
}

export function settledAnnouncer(
	announce: (text: string) => void,
	settleMs: number = STEP_ANNOUNCE_SETTLE_MS
): SettledAnnouncer {
	let timer: ReturnType<typeof setTimeout> | undefined;
	let waiting = "";
	let said = "";
	const cancel = () => {
		if (timer !== undefined) clearTimeout(timer);
		timer = undefined;
	};
	return {
		update(text) {
			// The same step again (a render that changed nothing else): its wait goes on.
			if (text === waiting) return;
			waiting = text;
			cancel();
			if (!text || text === said) return;
			timer = setTimeout(() => {
				timer = undefined;
				said = text;
				announce(text);
			}, settleMs);
		},
		dispose() {
			cancel();
			waiting = "";
		},
	};
}
