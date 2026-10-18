/**
 * What "Clear all" on a Notifications tab removes, and how it asks first.
 *
 * Each tab reads one or all of the three inboxes (one per server category);
 * clearing a tab clears it in each inbox it reads, with the server applying
 * the same filters its list does, so nothing outside the tab goes.
 */

export type NotificationTab = "all" | "mentions" | "announcements" | "unread";

export interface ClearScope {
	/** The Unread tab clears only the unread items; every other tab clears all of its items. */
	unreadOnly: boolean;
}

export function clearScopeFor(tab: NotificationTab): ClearScope {
	return { unreadOnly: tab === "unread" };
}

/**
 * A job still running (an upload being read, a connector syncing): its row
 * cannot be deleted, as the job is still updating it. The server's own rule
 * (surfsense_backend/app/notifications/api/deletion.py, JOB_RUNNING): Clear
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
	mentions: "Clear all mentions?",
	announcements: "Clear all announcements?",
	unread: "Clear all unread notifications?",
};

export function clearConfirmTitle(tab: NotificationTab): string {
	return CONFIRM_TITLE[tab];
}
