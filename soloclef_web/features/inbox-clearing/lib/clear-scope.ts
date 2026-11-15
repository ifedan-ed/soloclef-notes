/**
 * What "Clear all" on a Notifications tab removes, and how it asks first.
 *
 * Both tabs read the one inbox; the server applies the same filter its list
 * does, so clearing Unread never takes a read item.
 */

export type NotificationTab = "all" | "unread";

export interface ClearScope {
	/** The Unread tab clears only the unread items; All clears every item. */
	unreadOnly: boolean;
}

export function clearScopeFor(tab: NotificationTab): ClearScope {
	return { unreadOnly: tab === "unread" };
}

/**
 * A job still running (an upload being read, a connector syncing): its row
 * cannot be deleted, as the job is still updating it. The server's own rule
 * (soloclef_api/internal/notifications, the 409 for a running job): Clear
 * all leaves it and deleting it alone is a 409, so it gets no Delete here.
 */
export function isRunning(item: { metadata?: unknown }): boolean {
	const metadata = item.metadata as { status?: unknown } | null | undefined;
	return metadata?.status === "in_progress";
}

/** Whether an item of an inbox goes when that inbox is cleared with this scope. */
export function isCleared(item: { read: boolean; metadata?: unknown }, scope: ClearScope): boolean {
	return (!scope.unreadOnly || !item.read) && !isRunning(item);
}

const CONFIRM_TITLE: Record<NotificationTab, string> = {
	all: "Clear all notifications?",
	unread: "Clear all unread notifications?",
};

export function clearConfirmTitle(tab: NotificationTab): string {
	return CONFIRM_TITLE[tab];
}
