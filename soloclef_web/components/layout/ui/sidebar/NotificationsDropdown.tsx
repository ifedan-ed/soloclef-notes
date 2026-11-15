"use client";

import { Bell } from "lucide-react";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
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
import { InboxTypeIcon } from "@/features/inbox-item-icons/ui/inbox-type-icon";
import { inboxStoreLink, inboxTarget, retryHref } from "@/features/inbox-targets/lib/inbox-target";
import type { InboxItem } from "@/hooks/use-inbox";
import { useIsMobile } from "@/hooks/use-mobile";
import { cn } from "@/lib/utils";

/** The account's inbox, as the panel draws and changes it. */
export interface NotificationsDropdownData {
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

export function NotificationsDropdown({
	notifications,
	onCloseMobileSidebar,
	open: controlledOpen,
	onOpenChange,
}: NotificationsDropdownProps) {
	const router = useRouter();
	const isMobile = useIsMobile();
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

	const {
		unreadCount,
		loading: isLoading,
		loadMore,
		markAsRead,
		markAllAsRead,
		deleteItem: deleteInboxItem,
		clearAll,
	} = notifications;
	const isLoadingMore = !!notifications.loadingMore;
	const hasMore = !!notifications.hasMore;
	const unreadLabel = formatNotificationCount(unreadCount);
	const allCount = notifications.totalCount ?? 0;
	// Newest first; the Unread tab hides what has been read.
	const items = useMemo(
		() =>
			notifications.items
				.filter((item) => activeFilter !== "unread" || !item.read)
				.sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime()),
		[activeFilter, notifications.items]
	);

	const loadNextPage = useCallback(() => {
		if (!isLoadingMore && hasMore) loadMore?.();
	}, [hasMore, isLoadingMore, loadMore]);

	useEffect(() => {
		if (!open || isLoading || isLoadingMore || !hasMore) return;
		const root = scrollContainerRef.current;
		const target = loadMoreTriggerRef.current;
		if (!root || !target) return;

		const observer = new IntersectionObserver(
			(entries) => {
				if (entries[0]?.isIntersecting) {
					loadNextPage();
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
	}, [hasMore, isLoading, isLoadingMore, loadNextPage, open]);

	const markItemAsRead = useCallback(
		async (item: InboxItem) => {
			if (item.read) return;
			setMarkingAsReadId(item.id);
			try {
				await markAsRead(item.id);
			} finally {
				setMarkingAsReadId(null);
			}
		},
		[markAsRead]
	);

	const deleteItem = useCallback(
		(item: InboxItem) => {
			void deleteInboxItem(item.id);
		},
		[deleteInboxItem]
	);

	const handleItemClick = useCallback(
		async (item: InboxItem) => {
			await markItemAsRead(item);
			// An App Store update opens the listing in a new tab; the inbox stays where it is.
			const storeLink = inboxStoreLink(item);
			if (storeLink) {
				window.open(storeLink, "_blank", "noopener,noreferrer");
				return;
			}
			// An upload or a connector sync has nowhere to go: opening it only marks it read.
			const target = inboxTarget(item);
			if (!target) return;
			setOpen(false);
			onCloseMobileSidebar?.();
			router.push(target);
		},
		[markItemAsRead, onCloseMobileSidebar, router, setOpen]
	);

	// A failed Studio output's "Try again": its notebook's Studio, which runs it again once.
	const handleRetry = useCallback(
		async (item: InboxItem, href: string) => {
			await markItemAsRead(item);
			setOpen(false);
			onCloseMobileSidebar?.();
			router.push(href);
		},
		[markItemAsRead, onCloseMobileSidebar, router, setOpen]
	);

	const handleMarkAllAsRead = useCallback(async () => {
		if (unreadCount === 0 || markingAllAsRead) return;
		setMarkingAllAsRead(true);
		try {
			await markAllAsRead();
		} finally {
			setMarkingAllAsRead(false);
		}
	}, [markAllAsRead, markingAllAsRead, unreadCount]);

	const tabCount = activeFilter === "unread" ? unreadCount : allCount;
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
			await clearAll(clearScopeFor(activeFilter));
		} finally {
			setClearing(false);
			setConfirmingClear(false);
		}
	}, [activeFilter, clearAll, clearing]);

	const emptyStateCopy =
		activeFilter === "unread"
			? {
					title: "No unread notifications",
					description: "New status updates will appear here.",
				}
			: {
					title: "No notifications",
					description: "Uploads, Studio outputs and automation runs will appear here.",
				};

	const tabs: { value: NotificationFilter; label: string; count: number }[] = [
		{ value: "all", label: "All", count: allCount },
		{ value: "unread", label: "Unread", count: unreadCount },
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
			{unreadCount > 0 ? (
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
								disabled={unreadCount === 0 || markingAllAsRead}
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
							const retry = retryHref(item);
							const failed = (item.metadata as { status?: unknown }).status === "failed";
							return (
								<div
									key={item.id}
									className={cn(
										"group relative rounded-lg",
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
											"h-auto w-full min-w-0 items-start justify-start rounded-lg px-2 py-2 pr-9 text-left whitespace-normal",
											"hover:bg-transparent hover:text-accent-foreground"
										)}
									>
										<InboxTypeIcon type={item.type} failed={failed} className="mt-0.5" />
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
									{retry ? (
										<div className="-mt-1.5 pb-1.5 pl-6.5">
											{/* The button's own padding (px-2) sits left of its label, so the wrapper starts two units further in and the label lines up with the title above it. */}
											<Button
												type="button"
												variant="ghost"
												size="sm"
												onClick={() => handleRetry(item, retry)}
												className="h-7 px-2 text-xs text-primary"
											>
												Try again
											</Button>
										</div>
									) : null}
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
								onClick={loadNextPage}
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
