"use client";

import { useQuery } from "@tanstack/react-query";
import { useAtomValue, useSetAtom } from "jotai";
import { useHydrateAtoms } from "jotai/utils";
import { House, NotebookPen } from "lucide-react";
import { usePathname, useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useTheme } from "next-themes";
import { useCallback, useEffect, useMemo, useState } from "react";
import { toast } from "sonner";
import { statusInboxItemsAtom } from "@/atoms/inbox/status-inbox.atom";
import { rightPanelCollapsedAtom } from "@/atoms/layout/right-panel.atom";
import { currentUserAtom } from "@/atoms/user/user-query.atoms";
import { deleteWorkspaceMutationAtom } from "@/atoms/workspaces/workspace-mutation.atoms";
import { workspaceLimitsAtom, workspacesAtom } from "@/atoms/workspaces/workspace-query.atoms";
import { ActionLogDialog } from "@/components/agent-action-log/action-log-dialog";
import { ActiveChatStreamRunner } from "@/components/chat/active-chat-stream-runner";
import {
	AlertDialog,
	AlertDialogAction,
	AlertDialogCancel,
	AlertDialogContent,
	AlertDialogDescription,
	AlertDialogFooter,
	AlertDialogHeader,
	AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Spinner } from "@/components/ui/spinner";
import { notesPath } from "@/features/notes/lib/note-places";
import { useInbox } from "@/hooks/use-inbox";
import { notificationsApiService } from "@/lib/apis/notifications-api.service";
import { workspacesApiService } from "@/lib/apis/workspaces-api.service";
import { getLoginPath, logout } from "@/lib/auth-utils";
import { AppError } from "@/lib/error";
import { resetUser, trackLogout } from "@/lib/posthog/events";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { queryClient } from "@/lib/query-client/client";
import { workspaceHomePath } from "@/lib/workspace-home";
import {
	keyMentionsWorkspace,
	markWorkspaceRemoved,
	pickNextWorkspace,
	readRecentWorkspaces,
	rememberWorkspaceVisit,
	removedWorkspaceIds,
	unmarkWorkspaceRemoved,
} from "@/lib/workspace-removal";
import type { NavItem, Workspace } from "../types/layout.types";
import { CreateWorkspaceDialog } from "../ui/dialogs";
import { LayoutShell } from "../ui/shell";
import { workspacePageFrame } from "./workspace-page-frame";

interface LayoutDataProviderProps {
	workspaceId: string;
	initialRightPanelCollapsed: boolean;
	children: React.ReactNode;
}

export function LayoutDataProvider({
	workspaceId,
	initialRightPanelCollapsed,
	children,
}: LayoutDataProviderProps) {
	useHydrateAtoms([[rightPanelCollapsedAtom, initialRightPanelCollapsed]]);
	const t = useTranslations("dashboard");
	const tCommon = useTranslations("common");
	const tSidebar = useTranslations("sidebar");
	const router = useRouter();
	const pathname = usePathname();
	const { theme, setTheme } = useTheme();

	// Desktop Documents panel state. Mobile uses the /documents workspace route.

	// Atoms
	const { data: user } = useAtomValue(currentUserAtom);
	const {
		data: workspacesData,
		refetch: refetchWorkspaces,
		isSuccess: workspacesLoaded,
	} = useAtomValue(workspacesAtom);
	const { data: workspaceLimits } = useAtomValue(workspaceLimitsAtom);
	const { mutateAsync: deleteWorkspace } = useAtomValue(deleteWorkspaceMutationAtom);
	// Fetch current workspace as a fallback for the selector while the full list catches up.
	const { data: currentWorkspace, isPending: currentWorkspacePending } = useQuery({
		queryKey: cacheKeys.workspaces.detail(workspaceId),
		queryFn: () => workspacesApiService.getWorkspace({ id: Number(workspaceId) }),
		enabled: !!workspaceId,
	});

	// Search space dialog state
	const [isCreateWorkspaceDialogOpen, setIsCreateWorkspaceDialogOpen] = useState(false);

	const userId = user?.id ? String(user.id) : null;
	const numericSpaceId = Number(workspaceId) || null;

	// Batch-fetch unread counts for all categories in a single request
	// instead of one /unread-count call per category.
	const { data: batchUnread, isLoading: isBatchUnreadLoading } = useQuery({
		queryKey: cacheKeys.notifications.batchUnreadCounts(numericSpaceId),
		queryFn: () => notificationsApiService.getBatchUnreadCounts(numericSpaceId ?? undefined),
		enabled: !!userId && !!numericSpaceId,
		staleTime: 30_000,
	});

	const commentsInbox = useInbox(
		userId,
		numericSpaceId,
		"comments",
		batchUnread?.comments,
		!isBatchUnreadLoading
	);
	const statusInbox = useInbox(
		userId,
		numericSpaceId,
		"status",
		batchUnread?.status,
		!isBatchUnreadLoading
	);

	const announcementsInbox = useInbox(
		userId,
		numericSpaceId,
		"announcements",
		batchUnread?.announcements,
		!isBatchUnreadLoading
	);

	const totalUnreadCount =
		commentsInbox.unreadCount + statusInbox.unreadCount + announcementsInbox.unreadCount;

	// Sync status inbox items to a shared atom so child components
	// (e.g. ConnectorPopup) can read them without creating duplicate useInbox hooks.
	const setStatusInboxItems = useSetAtom(statusInboxItemsAtom);
	useEffect(() => {
		setStatusInboxItems(statusInbox.inboxItems);
	}, [statusInbox.inboxItems, setStatusInboxItems]);

	// Delete/Leave workspace dialog state
	const [showDeleteWorkspaceDialog, setShowDeleteWorkspaceDialog] = useState(false);
	const [showLeaveWorkspaceDialog, setShowLeaveWorkspaceDialog] = useState(false);
	const [workspaceToDelete, setWorkspaceToDelete] = useState<Workspace | null>(null);
	const [workspaceToLeave, setWorkspaceToLeave] = useState<Workspace | null>(null);
	const [isDeletingWorkspace, setIsDeletingWorkspace] = useState(false);
	const [isLeavingWorkspace, setIsLeavingWorkspace] = useState(false);
	// Asked to delete or leave the only workspace: say why not instead.
	const [lastWorkspaceNotice, setLastWorkspaceNotice] = useState<Workspace | null>(null);

	const workspaces: Workspace[] = useMemo(() => {
		if (!workspacesData || !Array.isArray(workspacesData)) return [];
		return workspacesData.map((space) => ({
			id: space.id,
			name: space.name,
			description: space.description,
			isOwner: space.is_owner,
			memberCount: space.member_count || 0,
			createdAt: space.created_at,
		}));
	}, [workspacesData]);
	const maxWorkspacesPerUser = workspaceLimits?.max_workspaces_per_user;
	const ownedWorkspaceCount = workspaces.reduce(
		(count, space) => count + (space.isOwner ? 1 : 0),
		0
	);
	const isAtWorkspaceLimit =
		maxWorkspacesPerUser !== undefined && ownedWorkspaceCount >= maxWorkspacesPerUser;

	// Find active workspace from list, falling back to the route-scoped detail query.
	const activeWorkspace: Workspace | null = useMemo(() => {
		if (!workspaceId) return null;
		const workspaceIdNumber = Number(workspaceId);
		const listedSpace = workspaces.find((s) => s.id === workspaceIdNumber);
		if (listedSpace) return listedSpace;
		if (!currentWorkspace || currentWorkspace.id !== workspaceIdNumber) return null;
		return {
			id: currentWorkspace.id,
			name: currentWorkspace.name,
			description: currentWorkspace.description,
			isOwner: false,
			memberCount: 0,
			createdAt: currentWorkspace.created_at,
		};
	}, [currentWorkspace, workspaceId, workspaces]);

	// Safety redirect: if the current workspace is no longer in the user's list
	// (e.g. deleted by background task, membership revoked), redirect to a valid space.
	useEffect(() => {
		if (!workspacesLoaded || !workspaceId || isDeletingWorkspace || isLeavingWorkspace) return;
		// A workspace just created is not in the (stale) list yet: wait for its
		// own detail before deciding it is gone, or creating one bounces home.
		if (!activeWorkspace && currentWorkspacePending) return;
		if (workspaces.length > 0 && !activeWorkspace) {
			const next = pickNextWorkspace(
				workspaces,
				[...removedWorkspaceIds(), Number(workspaceId)],
				readRecentWorkspaces()
			);
			router.replace(next === null ? "/dashboard" : workspaceHomePath(next));
		} else if (workspaces.length === 0 && workspacesLoaded && !activeWorkspace) {
			router.replace("/dashboard");
		}
	}, [
		workspacesLoaded,
		workspaceId,
		workspaces,
		activeWorkspace,
		currentWorkspacePending,
		isDeletingWorkspace,
		isLeavingWorkspace,
		router,
	]);

	// Remember the order workspaces were opened in, for where to land after a
	// delete or leave; and once the tab has moved on, drop what was cached for a
	// removed workspace (inactive queries only, so nothing on screen refetches).
	useEffect(() => {
		const current = Number(workspaceId);
		if (!current) return;
		rememberWorkspaceVisit(current);
		const gone = removedWorkspaceIds().filter((id) => id !== current);
		if (gone.length === 0) return;
		queryClient.removeQueries({
			type: "inactive",
			predicate: (query) => keyMentionsWorkspace(query.queryKey, gone),
		});
	}, [workspaceId]);

	// Navigation items: the notebooks home, at the top of the rail, then every
	// note in the workspace (the Notes page). Documents and outputs live inside
	// their notebooks; connectors and automations in workspace settings.
	// Notifications, settings, the workspace switcher and "Connect your agent"
	// live in the rail's account menu.
	const isNotebooksActive = pathname?.endsWith("/notebooks") === true;
	const isNotesActive = pathname?.endsWith("/notes") === true;
	const navItems: NavItem[] = useMemo(
		() => [
			{
				title: tSidebar("home"),
				url: workspaceHomePath(workspaceId),
				icon: House,
				isActive: isNotebooksActive,
			},
			{
				title: tSidebar("notes"),
				url: notesPath(workspaceId),
				icon: NotebookPen,
				isActive: isNotesActive,
			},
		],
		[workspaceId, tSidebar, isNotebooksActive, isNotesActive]
	);

	// Handlers
	const handleWorkspaceSelect = useCallback(
		(id: number) => {
			router.push(workspaceHomePath(id));
		},
		[router]
	);

	const handleAddWorkspace = useCallback(() => {
		if (isAtWorkspaceLimit) {
			toast.error(
				`Workspace limit reached. You can own at most ${maxWorkspacesPerUser} workspaces.`
			);
			return;
		}
		setIsCreateWorkspaceDialogOpen(true);
	}, [isAtWorkspaceLimit, maxWorkspacesPerUser]);

	const handleUserSettings = useCallback(() => {
		router.push(`/dashboard/${workspaceId}/user-settings/profile`);
	}, [router, workspaceId]);

	const handleWorkspaceSettings = useCallback(
		(space: Workspace) => {
			router.push(`/dashboard/${space.id}/workspace-settings`);
		},
		[router]
	);

	const handleWorkspaceDeleteClick = useCallback(
		(space: Workspace) => {
			// The backend refuses to delete or leave someone's only workspace.
			if (workspaces.filter((other) => other.id !== space.id).length === 0) {
				setLastWorkspaceNotice(space);
				return;
			}
			// If user is owner, show delete dialog; otherwise show leave dialog
			if (space.isOwner) {
				setWorkspaceToDelete(space);
				setShowDeleteWorkspaceDialog(true);
			} else {
				setWorkspaceToLeave(space);
				setShowLeaveWorkspaceDialog(true);
			}
		},
		[workspaces]
	);

	/**
	 * Once a workspace is deleted or left: take it out of the cached list at
	 * once and, if it was open, go straight to the workspace opened most
	 * recently (else the first). No stop at /dashboard, which could pick the
	 * removed one from a stale list.
	 */
	const moveOnFrom = useCallback(
		(removedId: number) => {
			queryClient.setQueriesData<unknown>({ queryKey: cacheKeys.workspaces.all }, (old: unknown) =>
				Array.isArray(old) ? old.filter((space: { id?: number }) => space?.id !== removedId) : old
			);
			void refetchWorkspaces();
			if (Number(workspaceId) !== removedId) return;
			const next = pickNextWorkspace(
				workspaces,
				[...removedWorkspaceIds(), removedId],
				readRecentWorkspaces()
			);
			router.replace(next === null ? "/dashboard" : workspaceHomePath(next));
		},
		[refetchWorkspaces, workspaceId, workspaces, router]
	);

	const confirmDeleteWorkspace = useCallback(async () => {
		if (!workspaceToDelete) return;
		setIsDeletingWorkspace(true);
		// Marked first: queries racing the delete must not toast its 403s.
		markWorkspaceRemoved(workspaceToDelete.id);
		try {
			await deleteWorkspace({ id: workspaceToDelete.id });
			moveOnFrom(workspaceToDelete.id);
		} catch (error) {
			unmarkWorkspaceRemoved(workspaceToDelete.id);
			console.error("Error deleting workspace:", error);
			toast.error(
				error instanceof AppError && error.status === 409
					? error.message
					: t.has("delete_space_error")
						? t("delete_space_error")
						: "Failed to delete workspace"
			);
		} finally {
			setIsDeletingWorkspace(false);
			setShowDeleteWorkspaceDialog(false);
			setWorkspaceToDelete(null);
		}
	}, [workspaceToDelete, deleteWorkspace, moveOnFrom, t]);

	const confirmLeaveWorkspace = useCallback(async () => {
		if (!workspaceToLeave) return;
		setIsLeavingWorkspace(true);
		markWorkspaceRemoved(workspaceToLeave.id);
		try {
			await workspacesApiService.leaveWorkspace(workspaceToLeave.id);
			moveOnFrom(workspaceToLeave.id);
		} catch (error) {
			unmarkWorkspaceRemoved(workspaceToLeave.id);
			console.error("Error leaving workspace:", error);
			toast.error(
				error instanceof AppError && error.status === 409
					? error.message
					: t.has("leave_error")
						? t("leave_error")
						: "Failed to leave workspace"
			);
		} finally {
			setIsLeavingWorkspace(false);
			setShowLeaveWorkspaceDialog(false);
			setWorkspaceToLeave(null);
		}
	}, [workspaceToLeave, moveOnFrom, t]);

	const handleNavItemClick = useCallback(
		(item: NavItem) => {
			router.push(item.url);
		},
		[router]
	);

	const handleSettings = useCallback(() => {
		router.push(`/dashboard/${workspaceId}/workspace-settings`);
	}, [router, workspaceId]);

	const handleManageMembers = useCallback(() => {
		router.push(`/dashboard/${workspaceId}/team`);
	}, [router, workspaceId]);

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

	const frame = workspacePageFrame(pathname);

	return (
		<>
			{/* Persistent host: keeps an in-flight chat turn streaming across
			    in-app navigation and aborts it only on workspace teardown. */}
			<ActiveChatStreamRunner />
			<LayoutShell
				workspaces={workspaces}
				activeWorkspaceId={Number(workspaceId)}
				onWorkspaceSelect={handleWorkspaceSelect}
				onWorkspaceDelete={handleWorkspaceDeleteClick}
				onWorkspaceSettings={handleWorkspaceSettings}
				onAddWorkspace={handleAddWorkspace}
				isAtWorkspaceLimit={isAtWorkspaceLimit}
				maxWorkspacesPerUser={maxWorkspacesPerUser}
				navItems={navItems}
				onNavItemClick={handleNavItemClick}
				user={{
					email: user?.email || "",
					name: user?.display_name || user?.email?.split("@")[0],
					avatarUrl: user?.avatar_url || undefined,
				}}
				onSettings={handleSettings}
				onManageMembers={handleManageMembers}
				onUserSettings={handleUserSettings}
				onLogout={handleLogout}
				theme={theme}
				setTheme={setTheme}
				workspacePanelViewportClassName={frame.viewportClassName}
				workspacePanelContentClassName={frame.contentClassName}
				notifications={{
					totalUnreadCount,
					comments: {
						items: commentsInbox.inboxItems,
						unreadCount: commentsInbox.unreadCount,
						totalCount: commentsInbox.totalCount,
						loading: commentsInbox.loading,
						loadingMore: commentsInbox.loadingMore,
						hasMore: commentsInbox.hasMore,
						loadMore: commentsInbox.loadMore,
						markAsRead: commentsInbox.markAsRead,
						markAllAsRead: commentsInbox.markAllAsRead,
						deleteItem: commentsInbox.deleteItem,
						clearAll: commentsInbox.clearAll,
					},
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

			{/* Delete Workspace Dialog */}
			<AlertDialog open={showDeleteWorkspaceDialog} onOpenChange={setShowDeleteWorkspaceDialog}>
				<AlertDialogContent className="sm:max-w-md">
					<AlertDialogHeader>
						<AlertDialogTitle>{t("delete_workspace")}</AlertDialogTitle>
						<AlertDialogDescription>
							{t("delete_space_confirm", { name: workspaceToDelete?.name || "" })}
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel disabled={isDeletingWorkspace}>
							{tCommon("cancel")}
						</AlertDialogCancel>
						<AlertDialogAction
							onClick={(e) => {
								e.preventDefault();
								confirmDeleteWorkspace();
							}}
							disabled={isDeletingWorkspace}
							className="relative bg-destructive text-destructive-foreground hover:bg-destructive/90"
						>
							<span className={isDeletingWorkspace ? "opacity-0" : ""}>{tCommon("delete")}</span>
							{isDeletingWorkspace && <Spinner size="sm" className="absolute" />}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>

			{/* Leave Workspace Dialog */}
			<AlertDialog open={showLeaveWorkspaceDialog} onOpenChange={setShowLeaveWorkspaceDialog}>
				<AlertDialogContent className="sm:max-w-md">
					<AlertDialogHeader>
						<AlertDialogTitle>{t("leave_title")}</AlertDialogTitle>
						<AlertDialogDescription>
							{t("leave_confirm", { name: workspaceToLeave?.name || "" })}
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel disabled={isLeavingWorkspace}>{tCommon("cancel")}</AlertDialogCancel>
						<AlertDialogAction
							onClick={(e) => {
								e.preventDefault();
								confirmLeaveWorkspace();
							}}
							disabled={isLeavingWorkspace}
							className="relative bg-destructive text-destructive-foreground hover:bg-destructive/90"
						>
							<span className={isLeavingWorkspace ? "opacity-0" : ""}>{t("leave")}</span>
							{isLeavingWorkspace && <Spinner size="sm" className="absolute" />}
						</AlertDialogAction>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>

			{/* The only workspace cannot be deleted or left */}
			<AlertDialog
				open={lastWorkspaceNotice !== null}
				onOpenChange={(open) => {
					if (!open) setLastWorkspaceNotice(null);
				}}
			>
				<AlertDialogContent className="sm:max-w-md" data-testid="last-workspace-notice">
					<AlertDialogHeader>
						<AlertDialogTitle>{t("last_workspace_title")}</AlertDialogTitle>
						<AlertDialogDescription>
							{lastWorkspaceNotice?.isOwner
								? t("last_workspace_delete", { name: lastWorkspaceNotice?.name ?? "" })
								: t("last_workspace_leave", { name: lastWorkspaceNotice?.name ?? "" })}
						</AlertDialogDescription>
					</AlertDialogHeader>
					<AlertDialogFooter>
						<AlertDialogCancel>{t("last_workspace_ok")}</AlertDialogCancel>
					</AlertDialogFooter>
				</AlertDialogContent>
			</AlertDialog>

			{/* Create Workspace Dialog */}
			<CreateWorkspaceDialog
				open={isCreateWorkspaceDialogOpen}
				onOpenChange={setIsCreateWorkspaceDialogOpen}
			/>

			{/* Agent action log + revert dialog */}
			<ActionLogDialog />
		</>
	);
}
