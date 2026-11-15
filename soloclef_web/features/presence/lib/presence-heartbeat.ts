/**
 * Tells the server while this tab is on screen, so it holds back phone
 * pushes the person would see here anyway (PUT /api/v1/presence). The
 * server forgets a tab's mark after 150 s, so a visible tab beats well
 * inside that; a tab going out of sight says so at once, rather than
 * leaving the phone silent until the mark runs out.
 *
 * Nothing is sent while the tab is hidden apart from that one "gone" beat:
 * a background tab must never count as the person being here.
 */

/** How often a visible tab beats: well under the server's 150 s, so one lost beat never lets the mark lapse. */
export const PRESENCE_BEAT_MS = 60_000;

/** The parts of `document` the heartbeat reads; a stand-in in tests. */
export interface PresenceDocument {
	readonly visibilityState: DocumentVisibilityState;
	addEventListener(type: "visibilitychange", listener: () => void): void;
	removeEventListener(type: "visibilitychange", listener: () => void): void;
}

/** The parts of `window` the heartbeat reads: the page being left or brought back from the back-forward cache. */
export interface PresenceWindow {
	addEventListener(type: "pagehide" | "pageshow", listener: () => void): void;
	removeEventListener(type: "pagehide" | "pageshow", listener: () => void): void;
}

/** Sends one beat. It must never throw: a lost beat is a missed hint, not an error. */
export type SendPresence = (active: boolean) => void;

/**
 * Starts beating for this tab and returns the function that stops it. Stopping
 * (leaving the signed-in pages) also says the tab is gone, if it had said
 * it was here.
 */
export function startPresenceHeartbeat({
	doc,
	win,
	send,
}: {
	doc: PresenceDocument;
	win: PresenceWindow;
	send: SendPresence;
}): () => void {
	let timer: ReturnType<typeof setInterval> | null = null;
	// Whether the server was last told this tab is here: "gone" is said once, and only after "here".
	let present = false;

	const here = () => {
		present = true;
		send(true);
		if (timer === null) timer = setInterval(() => send(true), PRESENCE_BEAT_MS);
	};
	const gone = () => {
		if (timer !== null) {
			clearInterval(timer);
			timer = null;
		}
		if (!present) return;
		present = false;
		send(false);
	};
	const follow = () => (doc.visibilityState === "visible" ? here() : gone());

	doc.addEventListener("visibilitychange", follow);
	win.addEventListener("pagehide", gone);
	// A page restored from the back-forward cache starts no new heartbeat of its own.
	win.addEventListener("pageshow", follow);
	if (doc.visibilityState === "visible") here();

	return () => {
		doc.removeEventListener("visibilitychange", follow);
		win.removeEventListener("pagehide", gone);
		win.removeEventListener("pageshow", follow);
		gone();
	};
}
