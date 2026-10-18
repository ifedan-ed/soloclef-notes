"use client";

import { useAtom } from "jotai";
import { Bell, ExternalLink, Megaphone } from "lucide-react";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { setTargetCommentIdAtom } from "@/atoms/chat/current-thread.atom";
import { Button } from "@/components/ui/button";
import {
	Drawer,
	DrawerContent,
	DrawerHandle,
	DrawerTitle,
	DrawerTrigger,
} from "@/components/ui/drawer";
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { isCommentReplyMetadata, isNewMentionMetadata } from "@/contracts/types/inbox.types";
import {
	type ClearScope,
	clearConfirmTitle,
	clearScopeFor,
	isCleared,
	isRunning,
	type NotificationTab,
} from "@/features/inbox-clearing/lib/clear-scope";
import { ClearAllConfirm } from "@/features/inbox-clearing/ui/clear-all-confirm";
import { NotificationItemMenu } from "@/features/inbox-clearing/ui/notification-item-menu";
import {
	announcementTarget,
	SITE_ANNOUNCEMENT_TYPE,
} from "@/features/site-announcements/lib/announcement-inbox";
import { MarkdownLiteView } from "@/features/site-announcements/ui/markdown-lite-view";
import type { InboxItem } from "@/hooks/use-inbox";
import { useIsMobile } from "@/hooks/use-mobile";
import { cn } from "@/lib/utils";
import { notebookPath } from "@/lib/workspace-home";

export interface NotificationsDataSource {
	items: InboxItem[];
	unreadCount: number;
	totalCount?: number;
	loading: boolean;
	loadingMore?: boolean;
	hasMore?: boolean;
	loadMore?: () => void;
	markAsRead: (id: number) => Promise<boolean>;
	markAllAsRead: () => Promise<boolean>;
	deleteItem: (id: number) => Promise<boolean>;
	/** Deletes this inbox's items on the server too, loaded or not. */
	clearAll: (scope: ClearScope) => Promise<boolean>;
}

export interface NotificationsDropdownData {
	totalUnreadCount: number;
	comments: NotificationsDataSource;
	status: NotificationsDataSource;
	/** Announcements from the site's administrators. */
	announcements?: NotificationsDataSource;
}

interface NotificationsDropdownProps {
	notifications: NotificationsDropdownData;
	onCloseMobileSidebar?: () => void;
	/** Controlled open state, so the account menu's "Notifications" can open the panel. */
	open?: boolean;
	onOpenChange?: (open: boolean) => void;
}

type NotificationFilter = NotificationTab;

function formatNotificationCount(count: number): string {
	if (count <= 999) {
		return count.toString();
	}
	const thousands = Math.floor(count / 1000);
	return `${thousands}k+`;
}

function formatTime(dateString: string): string {
	try {
		const date = new Date(dateString);
		const now = new Date();
		const diffMs = now.getTime() - date.getTime();
		const diffMins = Math.floor(diffMs / (1000 * 60));
		const diffHours = Math.floor(diffMs / (1000 * 60 * 60));
		const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24));

		if (diffMins < 1) return "now";
		if (diffMins < 60) return `${diffMins}m ago`;
		if (diffHours < 24) return `${diffHours}h ago`;
		if (diffDays < 7) return `${diffDays}d ago`;
		return `${Math.floor(diffDays / 7)}w ago`;
	} catch {
		return "now";
	}
}

function isCommentNotification(item: InboxItem): boolean {
	return item.type === "new_mention" || item.type === "comment_reply";
}

function isAnnouncement(item: InboxItem): boolean {
	return item.type === SITE_ANNOUNCEMENT_TYPE;
}

const NO_ANNOUNCEMENTS: NotificationsDataSource = {
	items: [],
	unreadCount: 0,
	totalCount: 0,
	loading: false,
	markAsRead: async () => true,
	markAllAsRead: async () => true,
	deleteItem: async () => true,
	clearAll: async () => true,
};

export function NotificationsDropdown({
	notifications,
	onCloseMobileSidebar,
	open: controlledOpen,
	onOpenChange,
}: NotificationsDropdownProps) {
	const router = useRouter();
	const isMobile = useIsMobile();
	const [, setTargetCommentId] = useAtom(setTargetCommentIdAtom);
	const [uncontrolledOpen, setUncontrolledOpen] = useState(false);
	const open = controlledOpen ?? uncontrolledOpen;
	const setOpen = useCallback(
		(next: boolean) => {
			if (controlledOpen === undefined) setUncontrolledOpen(next);
			onOpenChange?.(next);
		},
		[controlledOpen, onOpenChange]
	);
	const [activeFilter, setActiveFilter] = useState<NotificationFilter>("all");
	const [markingAsReadId, setMarkingAsReadId] = useState<number | null>(null);
	const [markingAllAsRead, setMarkingAllAsRead] = useState(false);
	const [confirmingClear, setConfirmingClear] = useState(false);
	const [clearing, setClearing] = useState(false);
	const scrollContainerRef = useRef<HTMLDivElement>(null);
	const loadMoreTriggerRef = useRef<HTMLDivElement>(null);

	const announcements = notifications.announcements ?? NO_ANNOUNCEMENTS;
	// The inboxes each tab draws from; "all" and "unread" read every inbox.
	const sources = useMemo(() => {
		if (activeFilter === "mentions") return [notifications.comments];
		if (activeFilter === "announcements") return [announcements];
		return [notifications.comments, notifications.status, announcements];
	}, [activeFilter, announcements, notifications.comments, notifications.status]);

	const unreadLabel = formatNotificationCount(notifications.totalUnreadCount);
	const allCount =
		(notifications.comments.totalCount ?? 0) +
		(notifications.status.totalCount ?? 0) +
		(announcements.totalCount ?? 0);
	const mentionsCount = notifications.comments.totalCount ?? notifications.comments.items.length;
	const announcementsCount = announcements.totalCount ?? announcements.items.length;
	const visibleUnreadCount =
		activeFilter === "mentions"
			? notifications.comments.unreadCount
			: activeFilter === "announcements"
				? announcements.unreadCount
				: notifications.totalUnreadCount;
	const isLoading = sources.some((source) => source.loading);
	const isLoadingMore = sources.some((source) => !!source.loadingMore);
	const hasMore = sources.some((source) => !!source.hasMore);
	const items = useMemo(
		() =>
			sources
				.flatMap((source) => source.items)
				.filter((item) => activeFilter !== "unread" || !item.read)
				.sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()),
		[activeFilter, sources]
	);

	const loadMoreForActiveFilter = useCallback(() => {
		if (isLoadingMore) return;
		for (const source of sources) {
			if (source.hasMore) source.loadMore?.();
		}
	}, [isLoadingMore, sources]);

	useEffect(() => {
		if (!open || isLoading || isLoadingMore || !hasMore) return;
		const root = scrollContainerRef.current;
		const target = loadMoreTriggerRef.current;
		if (!root || !target) return;

		const observer = new IntersectionObserver(
			(entries) => {
				if (entries[0]?.isIntersecting) {
					loadMoreForActiveFilter();
				}
			},
			{
				root,
				rootMargin: "120px",
				threshold: 0,
			}
		);

		observer.observe(target);
		return () => observer.disconnect();
	}, [hasMore, isLoading, isLoadingMore, loadMoreForActiveFilter, open]);

	const sourceOf = useCallback(
		(item: InboxItem) =>
			isCommentNotification(item)
				? notifications.comments
				: isAnnouncement(item)
					? announcements
					: notifications.status,
		[announcements, notifications.comments, notifications.status]
	);

	const markItemAsRead = useCallback(
		async (item: InboxItem) => {
			if (item.read) return;
			setMarkingAsReadId(item.id);
			try {
				await sourceOf(item).markAsRead(item.id);
			} finally {
				setMarkingAsReadId(null);
			}
		},
		[sourceOf]
	);

	const deleteItem = useCallback(
		(item: InboxItem) => {
			void sourceOf(item).deleteItem(item.id);
		},
		[sourceOf]
	);

	const handleItemClick = useCallback(
		async (item: InboxItem) => {
			await markItemAsRead(item);

			const target = announcementTarget(item);
			if (target) {
				if (target.kind === "web") {
					window.open(target.href, "_blank", "noopener,noreferrer");
				} else {
					setOpen(false);
					onCloseMobileSidebar?.();
					router.push(target.href);
				}
				return;
			}

			if (item.type === "new_mention" && isNewMentionMetadata(item.metadata)) {
				const threadId = item.metadata.thread_id;
				const commentId = item.metadata.comment_id;
				if (item.workspace_id && threadId) {
					if (commentId) setTargetCommentId(commentId);
					setOpen(false);
					onCloseMobileSidebar?.();
					router.push(notebookPath(item.workspace_id, item.metadata.notebook_id));
				}
				return;
			}

			if (item.type === "comment_reply" && isCommentReplyMetadata(item.metadata)) {
				const threadId = item.metadata.thread_id;
				const replyId = item.metadata.reply_id;
				if (item.workspace_id && threadId) {
					if (replyId) setTargetCommentId(replyId);
					setOpen(false);
					onCloseMobileSidebar?.();
					router.push(notebookPath(item.workspace_id, item.metadata.notebook_id));
				}
				return;
			}
		},
		[markItemAsRead, onCloseMobileSidebar, router, setTargetCommentId, setOpen]
	);

	const handleMarkAllAsRead = useCallback(async () => {
		if (visibleUnreadCount === 0 || markingAllAsRead) return;
		setMarkingAllAsRead(true);
		try {
			await Promise.all(sources.map((source) => source.markAllAsRead()));
		} finally {
			setMarkingAllAsRead(false);
		}
	}, [markingAllAsRead, sources, visibleUnreadCount]);

	const tabCount = (() => {
		if (activeFilter === "mentions") return mentionsCount;
		if (activeFilter === "announcements") return announcementsCount;
		if (activeFilter === "unread") return notifications.totalUnreadCount;
		return allCount;
	})();
	// Nothing Clear all would take: no item on the tab, or only running jobs' (never cleared), and none
	// unloaded (the tab's count no more than the items here; not hasMore, which on Unread counts pages of read ones).
	const nothingToClear =
		tabCount <= items.length && !items.some((item) => isCleared(item, clearScopeFor(activeFilter)));

	// A new tab, or the panel closing, drops a question asked about the old one.
	// biome-ignore lint/correctness/useExhaustiveDependencies: reset on these changes only
	useEffect(() => {
		setConfirmingClear(false);
	}, [activeFilter, open]);

	const handleClearAll = useCallback(async () => {
		if (clearing) return;
		setClearing(true);
		try {
			const scope = clearScopeFor(activeFilter);
			await Promise.all(sources.map((source) => source.clearAll(scope)));
		} finally {
			setClearing(false);
			setConfirmingClear(false);
		}
	}, [activeFilter, clearing, sources]);

	const emptyStateCopy =
		activeFilter === "mentions"
			? {
					title: "No mentions",
					description: "Mentions and replies will appear here.",
				}
			: activeFilter === "announcements"
				? {
						title: "No announcements",
						description: "Announcements from the administrators will appear here.",
					}
				: activeFilter === "unread"
					? {
							title: "No unread notifications",
							description: "New mentions and status updates will appear here.",
						}
					: {
							title: "No notifications",
							description: "Mentions, replies, and status updates will appear here.",
						};

	const tabs: { value: NotificationFilter; label: string; count: number }[] = [
		{ value: "all", label: "All", count: allCount },
		{ value: "mentions", label: "Mentions", count: mentionsCount },
		{ value: "announcements", label: "Announcements", count: announcementsCount },
		{ value: "unread", label: "Unread", count: notifications.totalUnreadCount },
	];

	const triggerButton = (
		<Button
			type="button"
			variant="ghost"
			size="icon"
			aria-label="Notifications"
			className={cn(
				"relative h-10 w-10 rounded-lg text-muted-foreground hover:bg-accent hover:text-accent-foreground",
				open && "bg-accent text-accent-foreground"
			)}
		>
			<Bell className="h-4 w-4" />
			{notifications.totalUnreadCount > 0 ? (
				<span className="absolute right-1 top-1 inline-flex h-4 min-w-4 items-center justify-center rounded-full bg-destructive px-1 text-[10px] font-semibold leading-none text-destructive-foreground">
					{unreadLabel}
				</span>
			) : null}
		</Button>
	);

	const panelContent = (
		<>
			<div className="flex h-14 shrink-0 items-center justify-between gap-3 border-b px-4">
				{confirmingClear ? (
					<ClearAllConfirm
						title={clearConfirmTitle(activeFilter)}
						busy={clearing}
						onCancel={() => setConfirmingClear(false)}
						onConfirm={handleClearAll}
					/>
				) : (
					<>
						<div className="min-w-0">
							<h2 className="text-base font-semibold">Notifications</h2>
						</div>
						<div className="flex shrink-0 items-center gap-1">
							<Button
								type="button"
								variant="ghost"
								size="sm"
								onClick={handleMarkAllAsRead}
								disabled={visibleUnreadCount === 0 || markingAllAsRead}
								className="h-8 shrink-0 gap-1.5 px-2 text-xs text-muted-foreground hover:text-accent-foreground"
							>
								{markingAllAsRead ? <Spinner size="xs" /> : null}
								Mark all read
							</Button>
							<Button
								type="button"
								variant="ghost"
								size="sm"
								onClick={() => setConfirmingClear(true)}
								disabled={nothingToClear || isLoading}
								className="h-8 shrink-0 px-2 text-xs text-muted-foreground hover:text-accent-foreground"
							>
								Clear all
							</Button>
						</div>
					</>
				)}
			</div>

			<div className="relative flex shrink-0 items-end gap-3 overflow-x-auto px-4 after:absolute after:inset-x-0 after:bottom-0 after:z-0 after:h-px after:bg-muted-foreground/25 after:content-['']">
				{tabs.map((tab) => {
					const isActive = activeFilter === tab.value;
					return (
						<button
							key={tab.value}
							type="button"
							aria-pressed={isActive}
							onClick={() => setActiveFilter(tab.value)}
							className={cn(
								"relative z-10 flex h-11 shrink-0 items-center gap-1.5 border-b-2 border-transparent px-0 text-sm font-medium text-muted-foreground transition-colors",
								"hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
								isActive && "border-primary text-primary"
							)}
						>
							<span>{tab.label}</span>
							<span className="inline-flex h-5 min-w-5 items-center justify-center rounded-full bg-muted px-1.5 text-[11px] font-semibold text-muted-foreground">
								{formatNotificationCount(tab.count)}
							</span>
						</button>
					);
				})}
			</div>

			<div ref={scrollContainerRef} className="min-h-0 flex-1 overflow-y-auto p-2">
				{isLoading ? (
					<div className="space-y-1">
						{[82, 64, 74].map((width) => (
							<div key={width} className="flex h-[72px] items-center rounded-lg px-2 py-2">
								<div className="min-w-0 flex-1 space-y-2">
									<Skeleton className="h-3 rounded" style={{ width: `${width}%` }} />
									<Skeleton className="h-2.5 w-1/2 rounded" />
								</div>
							</div>
						))}
					</div>
				) : items.length > 0 ? (
					<div className="space-y-1">
						{items.map((item) => {
							const isMarkingAsRead = markingAsReadId === item.id;
							if (isAnnouncement(item)) {
								const target = announcementTarget(item);
								return (
									<article
										key={item.id}
										aria-label={item.title}
										className={cn("group rounded-lg px-2 py-2", !item.read && "bg-accent/40")}
									>
										<div className="flex min-w-0 items-start gap-2">
											<Megaphone className="mt-0.5 h-3.5 w-3.5 shrink-0 text-primary" aria-hidden />
											<p
												className={cn("flex-1 text-sm font-medium", !item.read && "font-semibold")}
											>
												{item.title}
											</p>
											{!item.read ? (
												<span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-primary" />
											) : null}
											<NotificationItemMenu
												title={item.title}
												onDelete={() => deleteItem(item)}
												className="-mt-1 -mr-1"
											/>
										</div>
										<MarkdownLiteView
											source={item.message}
											className="mt-1 select-text pl-5.5 text-xs text-muted-foreground"
										/>
										<div className="mt-1 flex items-center gap-1 pl-5.5">
											<span className="mr-auto text-[11px] text-muted-foreground/70">
												{formatTime(item.created_at)}
											</span>
											{isMarkingAsRead ? <Spinner size="xs" /> : null}
											{!item.read ? (
												<Button
													type="button"
													variant="ghost"
													size="sm"
													disabled={isMarkingAsRead}
													onClick={() => markItemAsRead(item)}
													className="h-7 px-2 text-xs text-muted-foreground"
												>
													Mark read
												</Button>
											) : null}
											{target ? (
												<Button
													type="button"
													variant="ghost"
													size="sm"
													onClick={() => handleItemClick(item)}
													className="h-7 gap-1 px-2 text-xs text-primary"
												>
													Open
													{target.kind === "web" ? (
														<ExternalLink className="h-3 w-3" aria-hidden />
													) : null}
												</Button>
											) : null}
										</div>
									</article>
								);
							}
							return (
								<div
									key={item.id}
									className={cn(
										"group relative flex items-start rounded-lg",
										"hover:bg-accent hover:text-accent-foreground",
										!item.read && "bg-accent/40"
									)}
									style={{ contentVisibility: "auto", containIntrinsicSize: "0 72px" }}
								>
									<Button
										type="button"
										variant="ghost"
										disabled={isMarkingAsRead}
										onClick={() => handleItemClick(item)}
										className={cn(
											"h-auto min-w-0 flex-1 justify-start rounded-lg px-2 py-2 pr-9 text-left",
											"hover:bg-transparent hover:text-accent-foreground"
										)}
									>
										<div className="min-w-0 flex-1">
											<div className="flex min-w-0 items-start gap-2">
												<p
													className={cn(
														"line-clamp-1 flex-1 text-sm font-medium",
														!item.read && "font-semibold"
													)}
												>
													{item.title}
												</p>
												{!item.read ? (
													<span className="mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-primary" />
												) : null}
											</div>
											<p className="mt-0.5 line-clamp-2 text-xs font-normal text-muted-foreground group-hover:text-accent-foreground/80">
												{item.message}
											</p>
											<p className="mt-1 text-[11px] font-normal text-muted-foreground/70">
												{formatTime(item.created_at)}
											</p>
										</div>
										{isMarkingAsRead ? <Spinner size="xs" className="shrink-0" /> : null}
									</Button>
									{/* A running job's item cannot be deleted until it has finished (isRunning). */}
									{isRunning(item) ? null : (
										<NotificationItemMenu
											title={item.title}
											onDelete={() => deleteItem(item)}
											className="absolute top-1.5 right-1.5"
										/>
									)}
								</div>
							);
						})}
						{hasMore ? (
							<div
								ref={loadMoreTriggerRef}
								className="flex min-h-10 items-center justify-center py-2"
							>
								{isLoadingMore ? <Spinner size="xs" /> : null}
							</div>
						) : null}
					</div>
				) : (
					<div className="flex min-h-full flex-col items-center justify-center px-6 py-10 text-center">
						<p className="text-sm font-medium">{emptyStateCopy.title}</p>
						<p className="mt-1 text-xs text-muted-foreground">{emptyStateCopy.description}</p>
						{hasMore ? (
							<Button
								type="button"
								variant="ghost"
								size="sm"
								onClick={loadMoreForActiveFilter}
								disabled={isLoadingMore}
								className="mt-3 text-xs"
							>
								{isLoadingMore ? <Spinner size="xs" /> : null}
								Load more
							</Button>
						) : null}
					</div>
				)}
			</div>
		</>
	);

	if (isMobile) {
		return (
			<Drawer open={open} onOpenChange={setOpen} shouldScaleBackground={false}>
				<DrawerTrigger asChild>{triggerButton}</DrawerTrigger>
				<DrawerContent
					className="z-80 h-[78vh] max-h-[90vh] overflow-hidden rounded-t-2xl border bg-popover text-popover-foreground"
					overlayClassName="z-80"
				>
					<DrawerHandle className="mt-3 h-1.5 w-10" />
					<DrawerTitle className="sr-only">Notifications</DrawerTitle>
					<div className="flex min-h-0 flex-1 select-none flex-col">{panelContent}</div>
				</DrawerContent>
			</Drawer>
		);
	}

	return (
		<Popover open={open} onOpenChange={setOpen}>
			<Tooltip>
				<TooltipTrigger asChild>
					<PopoverTrigger asChild>{triggerButton}</PopoverTrigger>
				</TooltipTrigger>
				<TooltipContent side="right" sideOffset={8}>
					Notifications
				</TooltipContent>
			</Tooltip>
			<PopoverContent
				side="right"
				align="end"
				sideOffset={10}
				className="z-80 flex h-[min(480px,calc(100vh-2rem))] w-[400px] select-none flex-col overflow-hidden rounded-xl border bg-popover p-0 text-popover-foreground shadow-lg"
			>
				{panelContent}
			</PopoverContent>
		</Popover>
	);
}
