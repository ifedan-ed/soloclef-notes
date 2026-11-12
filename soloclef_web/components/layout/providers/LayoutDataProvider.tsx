"use client";

import { useQuery } from "@tanstack/react-query";
import { useAtomValue, useSetAtom } from "jotai";
import { useHydrateAtoms } from "jotai/utils";
import { House, NotebookPen } from "lucide-react";
import { usePathname, useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useTheme } from "next-themes";
import { useCallback, useEffect, useMemo } from "react";
import { statusInboxItemsAtom } from "@/atoms/inbox/status-inbox.atom";
import { rightPanelCollapsedAtom } from "@/atoms/layout/right-panel.atom";
import { currentUserAtom } from "@/atoms/user/user-query.atoms";
import { ActionLogDialog } from "@/components/agent-action-log/action-log-dialog";
import { ActiveChatStreamRunner } from "@/components/chat/active-chat-stream-runner";
import { useInbox } from "@/hooks/use-inbox";
import { notificationsApiService } from "@/lib/apis/notifications-api.service";
import { getLoginPath, logout } from "@/lib/auth-utils";
import { NOTEBOOKS_HOME, NOTES_PAGE, settingsPath } from "@/lib/dashboard-paths";
import { resetUser, trackLogout } from "@/lib/posthog/events";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import type { NavItem } from "../types/layout.types";
import { LayoutShell } from "../ui/shell";
import { pageFrame } from "./page-frame";

interface LayoutDataProviderProps {
	initialRightPanelCollapsed: boolean;
	children: React.ReactNode;
}

/**
 * What the app's frame needs, gathered once for every page: the account, its
 * notifications, the rail's links and the account menu's actions.
 */
export function LayoutDataProvider({
	initialRightPanelCollapsed,
	children,
}: LayoutDataProviderProps) {
	useHydrateAtoms([[rightPanelCollapsedAtom, initialRightPanelCollapsed]]);
	const tSidebar = useTranslations("sidebar");
	const router = useRouter();
	const pathname = usePathname();
	const { theme, setTheme } = useTheme();

	const { data: user } = useAtomValue(currentUserAtom);
	const userId = user?.id ? String(user.id) : null;

	// One request for every inbox's unread count, instead of one per inbox.
	const { data: batchUnread, isLoading: isBatchUnreadLoading } = useQuery({
		queryKey: cacheKeys.notifications.batchUnreadCounts(),
		queryFn: () => notificationsApiService.getBatchUnreadCounts(),
		enabled: !!userId,
		staleTime: 30_000,
	});

	const statusInbox = useInbox(userId, "status", batchUnread?.status, !isBatchUnreadLoading);
	const announcementsInbox = useInbox(
		userId,
		"announcements",
		batchUnread?.announcements,
		!isBatchUnreadLoading
	);
	const totalUnreadCount = statusInbox.unreadCount + announcementsInbox.unreadCount;

	// Share the status inbox through an atom so child components (the
	// connector popup) read it without a second useInbox of their own.
	const setStatusInboxItems = useSetAtom(statusInboxItemsAtom);
	useEffect(() => {
		setStatusInboxItems(statusInbox.inboxItems);
	}, [statusInbox.inboxItems, setStatusInboxItems]);

	// The rail: the notebooks home, then every note (the Notes page).
	// Documents and outputs live inside their notebooks; connectors and
	// automations in settings. Notifications, settings and "Connect your
	// agent" live in the rail's account menu.
	const path = (pathname ?? "").replace(/\/+$/, "");
	const isNotebooksActive = path === NOTEBOOKS_HOME;
	const isNotesActive = path === NOTES_PAGE || path.startsWith(`${NOTES_PAGE}/`);
	const navItems: NavItem[] = useMemo(
		() => [
			{
				title: tSidebar("home"),
				url: NOTEBOOKS_HOME,
				icon: House,
				isActive: isNotebooksActive,
			},
			{
				title: tSidebar("notes"),
				url: NOTES_PAGE,
				icon: NotebookPen,
				isActive: isNotesActive,
			},
		],
		[tSidebar, isNotebooksActive, isNotesActive]
	);

	const handleNavItemClick = useCallback(
		(item: NavItem) => {
			router.push(item.url);
		},
		[router]
	);

	const handleSettings = useCallback(() => {
		router.push(settingsPath("profile"));
	}, [router]);

	const handleLogout = useCallback(async () => {
		try {
			trackLogout();
			resetUser();

			// Revoke refresh token on server and clear all tokens from localStorage
			await logout();

			if (typeof window !== "undefined") {
				router.push(getLoginPath());
			}
		} catch (error) {
			console.error("Error during logout:", error);
			await logout();
			router.push(getLoginPath());
		}
	}, [router]);

	const frame = pageFrame(pathname);

	return (
		<>
			{/* Persistent host: keeps an in-flight chat turn streaming across
			    in-app navigation and aborts it only when the app unmounts. */}
			<ActiveChatStreamRunner />
			<LayoutShell
				navItems={navItems}
				onNavItemClick={handleNavItemClick}
				user={{
					email: user?.email || "",
					name: user?.display_name || user?.email?.split("@")[0],
					avatarUrl: user?.avatar_url || undefined,
				}}
				onSettings={handleSettings}
				onLogout={handleLogout}
				theme={theme}
				setTheme={setTheme}
				pageViewportClassName={frame.viewportClassName}
				pageContentClassName={frame.contentClassName}
				notifications={{
					totalUnreadCount,
					status: {
						items: statusInbox.inboxItems,
						unreadCount: statusInbox.unreadCount,
						totalCount: statusInbox.totalCount,
						loading: statusInbox.loading,
						loadingMore: statusInbox.loadingMore,
						hasMore: statusInbox.hasMore,
						loadMore: statusInbox.loadMore,
						markAsRead: statusInbox.markAsRead,
						markAllAsRead: statusInbox.markAllAsRead,
						deleteItem: statusInbox.deleteItem,
						clearAll: statusInbox.clearAll,
					},
					announcements: {
						items: announcementsInbox.inboxItems,
						unreadCount: announcementsInbox.unreadCount,
						totalCount: announcementsInbox.totalCount,
						loading: announcementsInbox.loading,
						loadingMore: announcementsInbox.loadingMore,
						hasMore: announcementsInbox.hasMore,
						loadMore: announcementsInbox.loadMore,
						markAsRead: announcementsInbox.markAsRead,
						markAllAsRead: announcementsInbox.markAllAsRead,
						deleteItem: announcementsInbox.deleteItem,
						clearAll: announcementsInbox.clearAll,
					},
				}}
				focusMode={frame.focusMode}
			>
				{children}
			</LayoutShell>

			{/* Agent action log + revert dialog */}
			<ActionLogDialog />
		</>
	);
}
