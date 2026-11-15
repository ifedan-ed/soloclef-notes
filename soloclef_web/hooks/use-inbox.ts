"use client";

import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import type { InboxItem, NotificationCategory } from "@/contracts/types/inbox.types";
import { type ClearScope, isCleared, isRunning } from "@/features/inbox-clearing/lib/clear-scope";
import { mergeInboxHead, stillHidden } from "@/features/inbox-refresh/lib/merge-inbox-head";
import { notificationsApiService } from "@/lib/apis/notifications-api.service";
import { NotFoundError } from "@/lib/error";
import { cacheKeys } from "@/lib/query-client/cache-keys";

export type {
	InboxItem,
	InboxItemTypeEnum,
	NotificationCategory,
} from "@/contracts/types/inbox.types";

const INITIAL_PAGE_SIZE = 50;
const SCROLL_PAGE_SIZE = 30;

const newestFirst = (a: InboxItem, b: InboxItem) =>
	new Date(b.created_at).getTime() - new Date(a.created_at).getTime();

/**
 * One inbox (a notification category): its items, counts and actions.
 *
 * The newest page is a query (`notifications.head`) that the change stream
 * reads again whenever notifications are added, read or deleted anywhere
 * (`inbox`, features/live-changes); each read is folded into what is shown
 * (merge-inbox-head.ts), keeping older pages loaded by scrolling. The unread
 * count is the server's (`serverUnread`, read again on the same change);
 * actions here move it at once and the next read confirms it.
 *
 * This replaces counting unread from a synced window of rows plus an offset
 * calibrated against the server, which drifted whenever the window and the
 * server disagreed.
 */
export function useInbox(
	userId: string | null,
	category: NotificationCategory,
	serverUnread: { total_unread: number } | undefined
) {
	const [inboxItems, setInboxItems] = useState<InboxItem[]>([]);
	const [loadingMore, setLoadingMore] = useState(false);
	const [unreadCount, setUnreadCount] = useState(0);
	const [totalCount, setTotalCount] = useState(0);
	// Deleted here, maybe not yet on the server when the next head is read: kept out meanwhile.
	const hiddenIdsRef = useRef<Set<number>>(new Set());
	// The category the shown items belong to, so a switch starts from nothing.
	const shownCategoryRef = useRef<NotificationCategory | null>(null);

	const head = useQuery({
		queryKey: cacheKeys.notifications.head(category),
		queryFn: () =>
			notificationsApiService.getNotifications({
				queryParams: { category, limit: INITIAL_PAGE_SIZE },
			}),
		enabled: !!userId,
		// The inbox shows its own "couldn't load" state.
		meta: { suppressGlobalErrorToast: true },
	});

	useEffect(() => {
		const page = head.data;
		if (!page) return;
		const sameCategory = shownCategoryRef.current === category;
		shownCategoryRef.current = category;
		hiddenIdsRef.current = stillHidden(hiddenIdsRef.current, page);
		setInboxItems((shown) => mergeInboxHead(sameCategory ? shown : [], page, hiddenIdsRef.current));
		setTotalCount(page.total);
	}, [head.data, category]);

	useEffect(() => {
		if (serverUnread) setUnreadCount(serverUnread.total_unread);
	}, [serverUnread]);

	// Load older pages (cursor: the oldest item shown).
	const loadMore = useCallback(async () => {
		if (loadingMore || !userId || inboxItems.length >= totalCount) return;

		setLoadingMore(true);
		try {
			const oldestItem = inboxItems.length > 0 ? inboxItems[inboxItems.length - 1] : null;
			const response = await notificationsApiService.getNotifications({
				queryParams: {
					category,
					before_date: oldestItem?.created_at ?? undefined,
					limit: SCROLL_PAGE_SIZE,
				},
			});
			const older = response.items.filter((item) => !hiddenIdsRef.current.has(item.id));
			setInboxItems((prev) => {
				const existingIds = new Set(prev.map((d) => d.id));
				return [...prev, ...older.filter((d) => !existingIds.has(d.id))];
			});
			setTotalCount(response.total);
		} catch (err) {
			console.error(`[useInbox:${category}] Load more failed:`, err);
		} finally {
			setLoadingMore(false);
		}
	}, [loadingMore, userId, inboxItems, totalCount, category]);

	// Mark single item as read with optimistic update
	const markAsRead = useCallback(
		async (itemId: number) => {
			const item = inboxItems.find((i) => i.id === itemId);
			if (!item || item.read) return true;

			const undo = () => {
				setInboxItems((prev) => prev.map((i) => (i.id === itemId ? { ...i, read: false } : i)));
				setUnreadCount((prev) => prev + 1);
			};
			setInboxItems((prev) => prev.map((i) => (i.id === itemId ? { ...i, read: true } : i)));
			setUnreadCount((prev) => Math.max(0, prev - 1));

			try {
				const result = await notificationsApiService.markAsRead({ notificationId: itemId });
				if (!result.success) undo();
				return result.success;
			} catch (err) {
				console.error("Failed to mark as read:", err);
				undo();
				return false;
			}
		},
		[inboxItems]
	);

	// Mark all as read with optimistic update
	const markAllAsRead = useCallback(async () => {
		const prevItems = inboxItems;
		const prevCount = unreadCount;
		const undo = () => {
			setInboxItems(prevItems);
			setUnreadCount(prevCount);
		};

		setInboxItems((prev) => prev.map((item) => ({ ...item, read: true })));
		setUnreadCount(0);

		try {
			const result = await notificationsApiService.markAllAsRead();
			if (!result.success) undo();
			return result.success;
		} catch (err) {
			console.error("Failed to mark all as read:", err);
			undo();
			return false;
		}
	}, [inboxItems, unreadCount]);

	// Delete one item: gone at once, back if the server refuses
	const deleteItem = useCallback(
		async (itemId: number) => {
			const item = inboxItems.find((i) => i.id === itemId);
			if (!item) return true;

			hiddenIdsRef.current.add(itemId);
			setInboxItems((prev) => prev.filter((i) => i.id !== itemId));
			setTotalCount((prev) => Math.max(0, prev - 1));
			if (!item.read) setUnreadCount((prev) => Math.max(0, prev - 1));

			try {
				await notificationsApiService.deleteNotification({ notificationId: itemId });
				return true;
			} catch (err) {
				// Already gone (another tab, the phone): the same outcome.
				if (err instanceof NotFoundError) return true;
				console.error("Failed to delete notification:", err);
				hiddenIdsRef.current.delete(itemId);
				setInboxItems((prev) =>
					prev.some((i) => i.id === itemId) ? prev : [...prev, item].sort(newestFirst)
				);
				setTotalCount((prev) => prev + 1);
				if (!item.read) setUnreadCount((prev) => prev + 1);
				return false;
			}
		},
		[inboxItems]
	);

	// Clear this inbox (all of it, or its unread items), loaded pages or not; a running job's items stay
	const clearAll = useCallback(
		async (scope: ClearScope) => {
			const prevItems = inboxItems;
			const prevTotal = totalCount;
			const prevUnread = unreadCount;

			// Hide what is cleared, also in the last head read, so a head read
			// before the server has cleared brings nothing back.
			const hiddenNow = [...prevItems, ...(head.data?.items ?? [])]
				.filter((item) => isCleared(item, scope))
				.map((item) => item.id)
				.filter((id) => !hiddenIdsRef.current.has(id));
			for (const id of hiddenNow) hiddenIdsRef.current.add(id);

			// A running job's items stay (the server keeps them too: clear-scope.ts, isRunning).
			const keptRunning = prevItems.filter(
				(item) => isRunning(item) && (!scope.unreadOnly || !item.read)
			);
			const keptRunningUnread = keptRunning.filter((item) => !item.read);
			setInboxItems((prev) => prev.filter((item) => !isCleared(item, scope)));
			setTotalCount(
				scope.unreadOnly
					? Math.max(0, prevTotal - prevUnread + keptRunningUnread.length)
					: keptRunning.length
			);
			setUnreadCount(keptRunningUnread.length);

			try {
				const result = await notificationsApiService.clearNotifications({
					queryParams: {
						category,
						filter: scope.unreadOnly ? "unread" : undefined,
					},
				});
				setTotalCount(Math.max(0, prevTotal - result.deleted_count));
				return true;
			} catch (err) {
				console.error("Failed to clear notifications:", err);
				for (const id of hiddenNow) hiddenIdsRef.current.delete(id);
				setInboxItems(prevItems);
				setTotalCount(prevTotal);
				setUnreadCount(prevUnread);
				return false;
			}
		},
		[inboxItems, totalCount, unreadCount, category, head.data]
	);

	return {
		inboxItems,
		unreadCount,
		totalCount,
		markAsRead,
		markAllAsRead,
		deleteItem,
		clearAll,
		loading: !!userId && head.isPending,
		loadingMore,
		hasMore: inboxItems.length < totalCount,
		loadMore,
		error: head.error,
	};
}
