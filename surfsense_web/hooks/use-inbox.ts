"use client";

import { useQuery } from "@rocicorp/zero/react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { InboxItem, NotificationCategory } from "@/contracts/types/inbox.types";
import { type ClearScope, isCleared, isRunning } from "@/features/inbox-clearing/lib/clear-scope";
import {
	withoutDeletedElsewhere,
	withoutHidden,
} from "@/features/inbox-clearing/lib/zero-deletions";
import { isExpiredAnnouncement } from "@/features/site-announcements/lib/announcement-inbox";
import { notificationsApiService } from "@/lib/apis/notifications-api.service";
import { NotFoundError } from "@/lib/error";
import { queries } from "@/zero/queries";

export type {
	InboxItem,
	InboxItemTypeEnum,
	NotificationCategory,
} from "@/contracts/types/inbox.types";

const INITIAL_PAGE_SIZE = 50;
const SCROLL_PAGE_SIZE = 30;
const SYNC_WINDOW_DAYS = 4;

const CATEGORY_TYPES: Record<NotificationCategory, string[]> = {
	comments: ["new_mention", "comment_reply"],
	status: [
		"connector_indexing",
		"connector_deletion",
		"document_processing",
		"insufficient_credits",
	],
	announcements: ["site_announcement"],
};

const newestFirst = (a: InboxItem, b: InboxItem) =>
	new Date(b.created_at).getTime() - new Date(a.created_at).getTime();

function getSyncCutoffDate(): string {
	const cutoff = new Date();
	cutoff.setDate(cutoff.getDate() - SYNC_WINDOW_DAYS);
	cutoff.setUTCHours(0, 0, 0, 0);
	return cutoff.toISOString();
}

/**
 * Hook for managing inbox items with API-first architecture + Zero real-time deltas.
 *
 * Architecture:
 * 1. API is the PRIMARY data source — fetches first page on mount with category filter
 * 2. Zero provides REAL-TIME updates (new items, status changes, read state)
 * 3. Unread count = olderUnreadOffset + recent unread from Zero
 * 4. Deletes: here at once (kept hidden until Zero hears of them); from
 *    elsewhere once Zero's complete result no longer has the row
 */
export function useInbox(
	userId: string | null,
	workspaceId: number | null,
	category: NotificationCategory,
	prefetchedUnread?: { total_unread: number; recent_unread: number } | null,
	prefetchedUnreadReady = true
) {
	const [inboxItems, setInboxItems] = useState<InboxItem[]>([]);
	const [loading, setLoading] = useState(true);
	const [loadingMore, setLoadingMore] = useState(false);
	const [hasMore, setHasMore] = useState(false);
	const [error, setError] = useState<Error | null>(null);
	const [unreadCount, setUnreadCount] = useState(0);
	const [totalCount, setTotalCount] = useState(0);

	const initialLoadDoneRef = useRef(false);
	const olderUnreadOffsetRef = useRef<number | null>(null);
	const apiUnreadTotalRef = useRef(0);
	// Deleted here, still in Zero until its delete arrives: never re-added meanwhile.
	const hiddenIdsRef = useRef<Set<number>>(new Set());
	// The rows on screen, for counting what Zero says was deleted elsewhere.
	const inboxItemsRef = useRef<InboxItem[]>([]);
	// Declared before the Zero effect, so it runs first in the same commit.
	useEffect(() => {
		inboxItemsRef.current = inboxItems;
	}, [inboxItems]);

	const categoryTypes = CATEGORY_TYPES[category];

	// EFFECT 1: Fetch first page + unread count from API with category filter
	useEffect(() => {
		if (!userId || !workspaceId) return;
		if (!prefetchedUnreadReady) return;

		let cancelled = false;

		setLoading(true);
		setInboxItems([]);
		setHasMore(false);
		setTotalCount(0);
		initialLoadDoneRef.current = false;
		olderUnreadOffsetRef.current = null;
		apiUnreadTotalRef.current = 0;

		const fetchInitialData = async () => {
			try {
				const notificationsPromise = notificationsApiService.getNotifications({
					queryParams: {
						workspace_id: workspaceId,
						category,
						limit: INITIAL_PAGE_SIZE,
					},
				});

				const unreadPromise = prefetchedUnread
					? Promise.resolve(prefetchedUnread)
					: notificationsApiService.getUnreadCount(workspaceId, undefined, category);

				const [notificationsResponse, unreadResponse] = await Promise.all([
					notificationsPromise,
					unreadPromise,
				]);

				if (cancelled) return;

				setInboxItems(notificationsResponse.items.filter((item) => !isExpiredAnnouncement(item)));
				setTotalCount(notificationsResponse.total);
				setHasMore(notificationsResponse.has_more);
				setUnreadCount(unreadResponse.total_unread);
				apiUnreadTotalRef.current = unreadResponse.total_unread;
				setError(null);
				initialLoadDoneRef.current = true;
			} catch (err) {
				if (cancelled) return;
				console.error(`[useInbox:${category}] Initial load failed:`, err);
				setError(err instanceof Error ? err : new Error("Failed to load notifications"));
			} finally {
				if (!cancelled) setLoading(false);
			}
		};

		fetchInitialData();
		return () => {
			cancelled = true;
		};
	}, [userId, workspaceId, category, prefetchedUnread, prefetchedUnreadReady]);

	// EFFECT 2: Zero real-time sync for notification updates
	const [zeroNotifications, zeroResult] = useQuery(
		queries.notifications.byUser({ userId: userId ?? "" })
	);
	const zeroComplete = zeroResult.type === "complete";
	const zeroIds = useMemo(
		() => new Set((zeroNotifications ?? []).map((item) => item.id)),
		[zeroNotifications]
	);

	useEffect(() => {
		if (!userId || !workspaceId || !zeroNotifications || !initialLoadDoneRef.current) return;

		const cutoff = new Date(getSyncCutoffDate());

		if (zeroComplete) {
			for (const id of hiddenIdsRef.current) {
				if (!zeroIds.has(id)) hiddenIdsRef.current.delete(id);
			}
			// Deleted in another tab or on the phone: out of the list and its counts.
			const gone = inboxItemsRef.current.filter((item) => !zeroIds.has(item.id));
			if (gone.length > 0) {
				setTotalCount((prev) => Math.max(0, prev - gone.length));
				const olderUnreadGone = gone.filter(
					(item) => !item.read && new Date(item.created_at) < cutoff
				).length;
				if (olderUnreadGone > 0 && olderUnreadOffsetRef.current !== null) {
					olderUnreadOffsetRef.current = Math.max(
						0,
						olderUnreadOffsetRef.current - olderUnreadGone
					);
				}
			}
		}

		const validItems = withoutHidden(zeroNotifications, hiddenIdsRef.current).filter((item) => {
			if (item.id == null) return false;
			if (!categoryTypes.includes(item.type)) return false;
			if (isExpiredAnnouncement(item)) return false;
			if (item.workspaceId !== null && item.workspaceId !== workspaceId) return false;
			return true;
		});

		const recentItems = validItems.filter((item) => new Date(item.createdAt) > cutoff);

		const liveIds = new Set(recentItems.map((d) => d.id));

		setInboxItems((prev) => {
			const prevIds = new Set(prev.map((d) => d.id));

			const newItems: InboxItem[] = recentItems
				.filter((d) => !prevIds.has(d.id))
				.map(
					(item) =>
						({
							id: item.id,
							user_id: item.userId,
							workspace_id: item.workspaceId ?? undefined,
							type: item.type,
							title: item.title,
							message: item.message,
							read: item.read,
							metadata: item.metadata as unknown as Record<string, unknown>,
							created_at: new Date(item.createdAt).toISOString(),
							updated_at: item.updatedAt ? new Date(item.updatedAt).toISOString() : null,
						}) as InboxItem
				);

			const liveById = new Map(recentItems.map((v) => [v.id, v]));

			let updated = prev.map((existing) => {
				const liveItem = liveById.get(existing.id);
				if (liveItem) {
					return {
						...existing,
						read: liveItem.read,
						title: liveItem.title,
						message: liveItem.message,
						metadata: liveItem.metadata as unknown as Record<string, unknown>,
					} as InboxItem;
				}
				return existing;
			});

			updated = updated.filter((item) => {
				if (new Date(item.created_at) < cutoff) return true;
				// Not in Zero at all: deleted, or not synced yet; decided once Zero is complete.
				if (!zeroIds.has(item.id)) return true;
				return liveIds.has(item.id);
			});
			updated = withoutDeletedElsewhere(updated, zeroIds, zeroComplete);

			if (newItems.length > 0) {
				return [...newItems, ...updated];
			}

			return updated;
		});

		// Calibrate older-unread offset on first Zero data
		if (olderUnreadOffsetRef.current === null) {
			const recentUnreadCount = recentItems.filter((item) => !item.read).length;
			olderUnreadOffsetRef.current = Math.max(0, apiUnreadTotalRef.current - recentUnreadCount);
		}

		if (olderUnreadOffsetRef.current !== null) {
			const recentUnreadCount = recentItems.filter((item) => !item.read).length;
			setUnreadCount(olderUnreadOffsetRef.current + recentUnreadCount);
		}
	}, [userId, workspaceId, zeroNotifications, zeroIds, zeroComplete, categoryTypes]);

	// Load more pages via API (cursor-based using before_date)
	const loadMore = useCallback(async () => {
		if (loadingMore || !hasMore || !userId || !workspaceId) return;

		setLoadingMore(true);
		try {
			const oldestItem = inboxItems.length > 0 ? inboxItems[inboxItems.length - 1] : null;
			const beforeDate = oldestItem?.created_at ?? undefined;

			const response = await notificationsApiService.getNotifications({
				queryParams: {
					workspace_id: workspaceId,
					category,
					before_date: beforeDate,
					limit: SCROLL_PAGE_SIZE,
				},
			});

			const newItems = withoutHidden(response.items, hiddenIdsRef.current);

			setInboxItems((prev) => {
				const existingIds = new Set(prev.map((d) => d.id));
				const deduped = newItems.filter((d) => !existingIds.has(d.id));
				return [...prev, ...deduped];
			});
			setTotalCount(response.total);
			setHasMore(response.has_more);
		} catch (err) {
			console.error(`[useInbox:${category}] Load more failed:`, err);
		} finally {
			setLoadingMore(false);
		}
	}, [loadingMore, hasMore, userId, workspaceId, inboxItems, category]);

	// Mark single item as read with optimistic update
	const markAsRead = useCallback(
		async (itemId: number) => {
			const item = inboxItems.find((i) => i.id === itemId);
			if (!item || item.read) return true;

			const cutoff = new Date(getSyncCutoffDate());
			const isOlderItem = new Date(item.created_at) < cutoff;

			setInboxItems((prev) => prev.map((i) => (i.id === itemId ? { ...i, read: true } : i)));
			setUnreadCount((prev) => Math.max(0, prev - 1));

			if (isOlderItem && olderUnreadOffsetRef.current !== null) {
				olderUnreadOffsetRef.current = Math.max(0, olderUnreadOffsetRef.current - 1);
			}

			try {
				const result = await notificationsApiService.markAsRead({ notificationId: itemId });
				if (!result.success) {
					setInboxItems((prev) => prev.map((i) => (i.id === itemId ? { ...i, read: false } : i)));
					setUnreadCount((prev) => prev + 1);
					if (isOlderItem && olderUnreadOffsetRef.current !== null) {
						olderUnreadOffsetRef.current += 1;
					}
				}
				return result.success;
			} catch (err) {
				console.error("Failed to mark as read:", err);
				setInboxItems((prev) => prev.map((i) => (i.id === itemId ? { ...i, read: false } : i)));
				setUnreadCount((prev) => prev + 1);
				if (isOlderItem && olderUnreadOffsetRef.current !== null) {
					olderUnreadOffsetRef.current += 1;
				}
				return false;
			}
		},
		[inboxItems]
	);

	// Mark all as read with optimistic update
	const markAllAsRead = useCallback(async () => {
		const prevItems = inboxItems;
		const prevCount = unreadCount;
		const prevOffset = olderUnreadOffsetRef.current;

		setInboxItems((prev) => prev.map((item) => ({ ...item, read: true })));
		setUnreadCount(0);
		olderUnreadOffsetRef.current = 0;

		try {
			const result = await notificationsApiService.markAllAsRead();
			if (!result.success) {
				setInboxItems(prevItems);
				setUnreadCount(prevCount);
				olderUnreadOffsetRef.current = prevOffset;
			}
			return result.success;
		} catch (err) {
			console.error("Failed to mark all as read:", err);
			setInboxItems(prevItems);
			setUnreadCount(prevCount);
			olderUnreadOffsetRef.current = prevOffset;
			return false;
		}
	}, [inboxItems, unreadCount]);

	// Delete one item: gone at once, back if the server refuses
	const deleteItem = useCallback(
		async (itemId: number) => {
			const item = inboxItems.find((i) => i.id === itemId);
			if (!item) return true;

			const cutoff = new Date(getSyncCutoffDate());
			const unreadOlderItem = !item.read && new Date(item.created_at) < cutoff;

			hiddenIdsRef.current.add(itemId);
			setInboxItems((prev) => prev.filter((i) => i.id !== itemId));
			setTotalCount((prev) => Math.max(0, prev - 1));
			if (!item.read) setUnreadCount((prev) => Math.max(0, prev - 1));
			if (unreadOlderItem && olderUnreadOffsetRef.current !== null) {
				olderUnreadOffsetRef.current = Math.max(0, olderUnreadOffsetRef.current - 1);
			}

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
				if (unreadOlderItem && olderUnreadOffsetRef.current !== null) {
					olderUnreadOffsetRef.current += 1;
				}
				return false;
			}
		},
		[inboxItems]
	);

	// Clear this inbox (all of it, or its unread items), loaded pages or not; a running job's items stay
	const clearAll = useCallback(
		async (scope: ClearScope) => {
			if (!workspaceId) return false;
			const prevItems = inboxItems;
			const prevTotal = totalCount;
			const prevUnread = unreadCount;
			const prevOffset = olderUnreadOffsetRef.current;
			const prevHasMore = hasMore;

			// Hide what Zero still has of this inbox, so a sync before its deletes arrive brings nothing back.
			const hiddenNow = [
				...prevItems.filter((item) => isCleared(item, scope)).map((item) => item.id),
				...(zeroNotifications ?? [])
					.filter(
						(item) =>
							categoryTypes.includes(item.type) &&
							(item.workspaceId === null || item.workspaceId === workspaceId) &&
							isCleared(item, scope)
					)
					.map((item) => item.id),
			].filter((id) => !hiddenIdsRef.current.has(id));
			for (const id of hiddenNow) hiddenIdsRef.current.add(id);

			// A running job's items stay (the server keeps them too: clear-scope.ts, isRunning).
			const cutoff = new Date(getSyncCutoffDate());
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
			olderUnreadOffsetRef.current = keptRunningUnread.filter(
				(item) => new Date(item.created_at) < cutoff
			).length;
			if (!scope.unreadOnly) setHasMore(false);

			try {
				const result = await notificationsApiService.clearNotifications({
					queryParams: {
						workspace_id: workspaceId,
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
				olderUnreadOffsetRef.current = prevOffset;
				setHasMore(prevHasMore);
				return false;
			}
		},
		[
			inboxItems,
			totalCount,
			unreadCount,
			hasMore,
			workspaceId,
			category,
			categoryTypes,
			zeroNotifications,
		]
	);

	return {
		inboxItems,
		unreadCount,
		totalCount,
		markAsRead,
		markAllAsRead,
		deleteItem,
		clearAll,
		loading,
		loadingMore,
		hasMore,
		loadMore,
		error,
	};
}
