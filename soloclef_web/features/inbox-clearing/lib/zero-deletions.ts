/**
 * Notifications deleted somewhere else (another tab, the phone, an administrator
 * withdrawing an announcement) leave the open inbox.
 *
 * Zero syncs every one of the user's notifications (zero/queries/inbox.ts has no
 * window or limit), so once its result is complete, an item it no longer has
 * was deleted. Before that its rows may still be arriving, so nothing is dropped.
 */
export function withoutDeletedElsewhere<T extends { id: number }>(
	items: T[],
	zeroIds: ReadonlySet<number>,
	zeroComplete: boolean
): T[] {
	if (!zeroComplete) return items;
	const kept = items.filter((item) => zeroIds.has(item.id));
	return kept.length === items.length ? items : kept;
}

/**
 * Items deleted here stay out while Zero has not yet heard of the delete, so a
 * sync in between does not bring them back for a moment.
 */
export function withoutHidden<T extends { id: number }>(
	items: readonly T[],
	hidden: ReadonlySet<number>
): T[] {
	if (hidden.size === 0) return [...items];
	return items.filter((item) => !hidden.has(item.id));
}
