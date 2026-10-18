"use client";

import { Plus } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import type { NavItem, User, Workspace } from "../../types/layout.types";
import {
	NotificationsDropdown,
	type NotificationsDropdownData,
} from "../sidebar/NotificationsDropdown";
import { SidebarUserProfile } from "../sidebar/SidebarUserProfile";
import { WorkspaceAvatar } from "./WorkspaceAvatar";

interface IconRailProps {
	workspaces: Workspace[];
	activeWorkspaceId: number | null;
	onWorkspaceSelect: (id: number) => void;
	onWorkspaceDelete?: (workspace: Workspace) => void;
	onWorkspaceSettings?: (workspace: Workspace) => void;
	onAddWorkspace: () => void;
	isAtWorkspaceLimit?: boolean;
	maxWorkspacesPerUser?: number;
	/** Page links shown at the top of the rail (Home). */
	navItems?: NavItem[];
	onNavItemClick?: (item: NavItem) => void;
	user: User;
	/** The open workspace's settings and members, in the account menu. */
	onSettings?: () => void;
	onManageMembers?: () => void;
	onUserSettings?: () => void;
	notifications?: NotificationsDropdownData;
	onLogout?: () => void;
	theme?: string;
	setTheme?: (theme: "light" | "dark" | "system") => void;
	className?: string;
}

/**
 * The app's only sidebar: Home, the workspaces (click one to open its home),
 * add a workspace, then notifications and the account menu. The account menu
 * holds the workspace switcher, workspace and user settings, and "Connect
 * your agent".
 */
export function IconRail({
	workspaces,
	activeWorkspaceId,
	onWorkspaceSelect,
	onWorkspaceDelete,
	onWorkspaceSettings,
	onAddWorkspace,
	isAtWorkspaceLimit = false,
	maxWorkspacesPerUser,
	navItems = [],
	onNavItemClick,
	user,
	onSettings,
	onManageMembers,
	onUserSettings,
	notifications,
	onLogout,
	theme,
	setTheme,
	className,
}: IconRailProps) {
	const t = useTranslations("sidebar");
	const activeWorkspace = workspaces.find((w) => w.id === activeWorkspaceId);
	// The bell's panel, also opened from "Notifications" in the account menu.
	const [notificationsOpen, setNotificationsOpen] = useState(false);
	const addWorkspaceLabel =
		isAtWorkspaceLimit && maxWorkspacesPerUser !== undefined
			? t("workspace_limit_reached", { max: maxWorkspacesPerUser })
			: t("add_workspace");

	return (
		<nav
			aria-label={t("navigation")}
			className={cn("flex h-full w-14 min-h-0 flex-col items-center", className)}
		>
			{navItems.length > 0 && (
				<div className="flex w-full flex-col items-center gap-2 px-1.5 pt-3">
					{navItems.map(({ url, title, icon: Icon, isActive }) => (
						<Tooltip key={url}>
							<TooltipTrigger asChild>
								<Button
									variant="ghost"
									size="icon"
									onClick={() => onNavItemClick?.({ url, title, icon: Icon, isActive })}
									aria-current={isActive ? "page" : undefined}
									className={cn(
										"h-10 w-10 rounded-lg text-muted-foreground",
										"focus-visible:ring-2 focus-visible:ring-ring",
										isActive && "bg-accent text-accent-foreground"
									)}
								>
									<Icon className="h-5 w-5" />
									<span className="sr-only">{title}</span>
								</Button>
							</TooltipTrigger>
							<TooltipContent side="right" sideOffset={8}>
								{title}
							</TooltipContent>
						</Tooltip>
					))}
					<div className="mt-1 h-px w-8 bg-border/60" />
				</div>
			)}
			<ScrollArea className="w-full min-h-0 flex-1">
				<ul aria-label={t("workspaces")} className="flex flex-col items-center gap-2 px-1.5 py-3">
					{workspaces.map((workspace) => (
						<li key={workspace.id}>
							<WorkspaceAvatar
								name={workspace.name}
								isActive={workspace.id === activeWorkspaceId}
								isShared={workspace.memberCount > 1}
								isOwner={workspace.isOwner}
								onClick={() => onWorkspaceSelect(workspace.id)}
								onDelete={onWorkspaceDelete ? () => onWorkspaceDelete(workspace) : undefined}
								onSettings={onWorkspaceSettings ? () => onWorkspaceSettings(workspace) : undefined}
								size="md"
							/>
						</li>
					))}

					<li>
						<Tooltip>
							<TooltipTrigger asChild>
								<span className="inline-flex">
									<Button
										variant="ghost"
										size="icon"
										onClick={onAddWorkspace}
										disabled={isAtWorkspaceLimit}
										className="h-10 w-10 rounded-lg border-2 border-dashed border-muted-foreground/30 hover:border-muted-foreground/50 disabled:opacity-50"
									>
										<Plus className="h-5 w-5 text-muted-foreground" />
										<span className="sr-only">{addWorkspaceLabel}</span>
									</Button>
								</span>
							</TooltipTrigger>
							<TooltipContent side="right" sideOffset={8}>
								{addWorkspaceLabel}
							</TooltipContent>
						</Tooltip>
					</li>
				</ul>
			</ScrollArea>
			<SidebarUserProfile
				user={user}
				workspace={{
					workspaces,
					activeWorkspaceId,
					onWorkspaceSelect,
					onManageMembers,
					onWorkspaceSettings: onSettings,
					activeIsOwner: activeWorkspace?.isOwner,
					onDeleteOrLeave:
						onWorkspaceDelete && activeWorkspace
							? () => onWorkspaceDelete(activeWorkspace)
							: undefined,
				}}
				showConnectAgent
				onUserSettings={onUserSettings}
				onNotifications={notifications ? () => setNotificationsOpen(true) : undefined}
				notificationsUnreadCount={notifications?.totalUnreadCount}
				onLogout={onLogout}
				isCollapsed
				theme={theme}
				setTheme={setTheme}
				topContent={
					notifications ? (
						<NotificationsDropdown
							notifications={notifications}
							open={notificationsOpen}
							onOpenChange={setNotificationsOpen}
						/>
					) : undefined
				}
			/>
		</nav>
	);
}
