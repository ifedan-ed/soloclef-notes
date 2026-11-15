/**
 * Folds a fresh read of an inbox's newest page (its "head") into the items
 * the inbox already shows, which may include older pages loaded by scrolling.
 *
 * The head is the server's truth for everything as new as its oldest item:
 * an item in that range the head lacks was deleted (in another tab, on the
 * phone, by a clear). Items older than the head's range were loaded by
 * scrolling and stay as they are; when the head has no more after it, it is
 * the whole inbox and nothing older remains.
 */

export interface InboxListItem {
	id: number;
	created_at: string;
}

export interface InboxHead<T extends InboxListItem> {
	items: readonly T[];
	has_more: boolean;
}

const time = (item: InboxListItem) => new Date(item.created_at).getTime();
const newestFirst = (a: InboxListItem, b: InboxListItem) => time(b) - time(a);

/**
 * The merged list, newest first. `hidden` are items deleted here whose delete
 * the server may not have applied when the head was read: they stay out.
 */
export function mergeInboxHead<T extends InboxListItem>(
	shown: readonly T[],
	head: InboxHead<T>,
	hidden: ReadonlySet<number>
): T[] {
	const fresh = head.items.filter((item) => !hidden.has(item.id));
	if (!head.has_more || head.items.length === 0) return [...fresh].sort(newestFirst);
	const oldestInHead = Math.min(...head.items.map(time));
	const freshIds = new Set(fresh.map((item) => item.id));
	const older = shown.filter(
		(item) => time(item) < oldestInHead && !freshIds.has(item.id) && !hidden.has(item.id)
	);
	return [...fresh, ...older].sort(newestFirst);
}

/**
 * The ids still worth hiding: once a head is read without an item, the
 * server has applied its delete and the id can be forgotten.
 */
export function stillHidden(
	hidden: ReadonlySet<number>,
	head: InboxHead<InboxListItem>
): Set<number> {
	const inHead = new Set(head.items.map((item) => item.id));
	return new Set([...hidden].filter((id) => inHead.has(id)));
}
