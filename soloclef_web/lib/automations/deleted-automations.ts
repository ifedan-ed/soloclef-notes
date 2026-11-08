/**
 * Automations deleted in this browser session.
 *
 * Deleting an automation from its own page left its detail and runs queries
 * mounted for a moment; their refetch answered 404 and the global handler
 * showed "automation N not found" after the success toast. Queries for an
 * automation marked here are dropped from the cache, and any 404 they still
 * produce is expected, so it is not shown.
 */

const deleted = new Set<number>();

export function markAutomationDeleted(id: number): void {
	deleted.add(id);
}

/** Undo a mark when the delete request failed. */
export function unmarkAutomationDeleted(id: number): void {
	deleted.delete(id);
}

/** Whether ``key`` is a detail or runs query of a deleted automation. */
export function isDeletedAutomationKey(key: readonly unknown[] | undefined): boolean {
	if (!key || key[0] !== "automations") return false;
	if (key[1] !== "detail" && key[1] !== "runs") return false;
	const id = Number(key[2]);
	return Number.isFinite(id) && deleted.has(id);
}
