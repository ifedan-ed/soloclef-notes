/**
 * What happens in the browser after a workspace is deleted or left.
 *
 * - Where to go: the most recently opened workspace that is left, else the
 *   first one listed. Never the one just removed.
 * - Quiet failures: queries that still point at the removed workspace answer
 *   403 or 404 until they are dropped. Those are expected, so no error toast.
 *
 * The picking and matching are pure functions so they are tested without a
 * browser; the visit history lives in localStorage and survives its absence.
 */

import { AppError } from "./error";

/** The backend's answer when you are not (or no longer) a member. */
export const WORKSPACE_ACCESS_DENIED = "You don't have access to this workspace";

const RECENT_KEY = "soloclef.recent-workspaces";
const RECENT_LIMIT = 20;

const removed = new Set<number>();

export function markWorkspaceRemoved(id: number): void {
	removed.add(id);
}

/** Undo a mark when the delete or leave request failed. */
export function unmarkWorkspaceRemoved(id: number): void {
	removed.delete(id);
}

export function removedWorkspaceIds(): number[] {
	return [...removed];
}

/** Most recent first, without duplicates; ``id`` moves to the front. */
export function withVisit(recent: readonly number[], id: number): number[] {
	return [id, ...recent.filter((other) => other !== id)].slice(0, RECENT_LIMIT);
}

/**
 * The workspace to open once ``removedIds`` are gone: the most recent visit
 * still in the list, else the first listed; null when none is left.
 */
export function pickNextWorkspace(
	workspaces: readonly { id: number }[],
	removedIds: readonly number[],
	recent: readonly number[] = []
): number | null {
	const gone = new Set(removedIds);
	const remaining = workspaces.filter((space) => !gone.has(space.id));
	if (remaining.length === 0) return null;
	const listed = new Set(remaining.map((space) => space.id));
	const visited = recent.find((id) => listed.has(id) && !gone.has(id));
	return visited ?? remaining[0].id;
}

/** Whether a query or mutation key names one of ``ids`` (as a number or a string). */
export function keyMentionsWorkspace(
	key: readonly unknown[] | undefined,
	ids: readonly number[]
): boolean {
	if (!key || ids.length === 0) return false;
	const wanted = new Set<unknown>([...ids, ...ids.map(String)]);
	return key.some((part) => wanted.has(part));
}

/**
 * An error that only says the removed workspace is gone: a 403 or 404 from a
 * request keyed by it, or the backend's "no access to this workspace", once
 * something was removed in this tab.
 */
export function isRemovedWorkspaceError(
	error: unknown,
	key: readonly unknown[] | undefined,
	ids: readonly number[] = removedWorkspaceIds()
): boolean {
	if (ids.length === 0 || !(error instanceof AppError)) return false;
	if (error.status !== 403 && error.status !== 404) return false;
	return keyMentionsWorkspace(key, ids) || error.message === WORKSPACE_ACCESS_DENIED;
}

export function readRecentWorkspaces(): number[] {
	try {
		const raw = window.localStorage.getItem(RECENT_KEY);
		const parsed: unknown = raw ? JSON.parse(raw) : [];
		return Array.isArray(parsed) ? parsed.filter((id) => Number.isInteger(id)) : [];
	} catch {
		return [];
	}
}

export function rememberWorkspaceVisit(id: number): void {
	if (!Number.isInteger(id) || id <= 0) return;
	try {
		window.localStorage.setItem(RECENT_KEY, JSON.stringify(withVisit(readRecentWorkspaces(), id)));
	} catch {
		// Private windows and blocked storage: the first listed workspace is fine.
	}
}
