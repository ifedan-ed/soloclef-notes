/**
 * At most one reload per build in a short time, so that a chunk that is
 * missing even after a reload (the server down, a broken deploy) shows the
 * error page instead of reloading forever.
 *
 * Remembered in sessionStorage, which is this tab's own and survives the
 * reload. Keyed by the build: a tab that reloaded into a new build has not
 * yet reloaded for that one, so a deploy that follows soon after still gets
 * its one reload. The time lets a long-open tab reload again for a later,
 * unrelated failure of the same build.
 */

/** Long enough to span a slow reload and the page failing again after it. */
export const RELOAD_GUARD_MS = 5 * 60 * 1000;

const KEY_PREFIX = "soloclef:chunk-reload:";

type ReadableStore = Pick<Storage, "getItem">;
type WritableStore = Pick<Storage, "getItem" | "setItem">;

/** This tab's sessionStorage, or null where the browser refuses it (some private modes). */
export function tabStorage(): Storage | null {
	try {
		return typeof window === "undefined" ? null : window.sessionStorage;
	} catch {
		return null;
	}
}

/**
 * Whether this build may still reload for a missing chunk. With no storage
 * there is no way to tell a first failure from a loop, so it may not.
 */
export function mayReload(store: ReadableStore | null, buildId: string, now: number): boolean {
	if (!store) return false;
	try {
		const last = Number(store.getItem(KEY_PREFIX + buildId));
		// Nothing stored reads as 0, and an unreadable value as NaN: neither
		// is a recent reload.
		return !(last > 0 && now - last < RELOAD_GUARD_MS);
	} catch {
		return false;
	}
}

/** Take this build's one reload: true when it was free and is now recorded. */
export function claimReload(store: WritableStore | null, buildId: string, now: number): boolean {
	if (!store || !mayReload(store, buildId, now)) return false;
	try {
		store.setItem(KEY_PREFIX + buildId, String(now));
		return true;
	} catch {
		// Not recorded means not guarded: no reload.
		return false;
	}
}
