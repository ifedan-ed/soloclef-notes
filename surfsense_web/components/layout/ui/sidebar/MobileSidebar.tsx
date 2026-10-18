"use client";

import { PanelLeft, Plus } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import { cn } from "@/lib/utils";
import type { NavItem, User, Workspace } from "../../types/layout.types";
import { WorkspaceAvatar } from "../icon-rail/WorkspaceAvatar";
import { NotificationsDropdown, type NotificationsDropdownData } from "./NotificationsDropdown";
import { SidebarUserProfile } from "./SidebarUserProfile";

interface MobileSidebarProps {
	isOpen: boolean;
	onOpenChange: (open: boolean) => void;
	workspaces: Workspace[];
	activeWorkspaceId: number | null;
	onWorkspaceSelect: (id: number) => void;
	onAddWorkspace: () => void;
	isAtWorkspaceLimit?: boolean;
	maxWorkspacesPerUser?: number;
	navItems: NavItem[];
	onNavItemClick?: (item: NavItem) => void;
	user: User;
	onSettings?: () => void;
	/** Delete (owner) or leave (member) the open workspace; a dialog confirms. */
	onWorkspaceDelete?: (workspace: Workspace) => void;
	onManageMembers?: () => void;
	onUserSettings?: () => void;
	notifications?: NotificationsDropdownData;
	onLogout?: () => void;
	theme?: string;
	setTheme?: (theme: "light" | "dark" | "system") => void;
}

export function MobileSidebarTrigger({ onClick }: { onClick: () => void }) {
	return (
		<Button
			variant="ghost"
			size="icon"
			onClick={onClick}
			className="md:hidden h-8 w-8 shrink-0 text-muted-foreground hover:bg-transparent hover:text-muted-foreground"
		>
			<PanelLeft className="h-4 w-4" />
			<span className="sr-only">Open menu</span>
		</Button>
	);
}

const rowClassName = cn(
	"flex h-11 w-full items-center gap-3 rounded-lg px-2 text-left text-sm transition-colors",
	"hover:bg-accent hover:text-accent-foreground",
	"focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
);

/**
 * The phone's navigation sheet: the desktop rail with its labels written out
 * (Home, the workspaces, add a workspace), with the bell and the account menu.
 */
export function MobileSidebar({
	isOpen,
	onOpenChange,
	workspaces,
	activeWorkspaceId,
	onWorkspaceSelect,
	onAddWorkspace,
	isAtWorkspaceLimit = false,
	maxWorkspacesPerUser,
	navItems,
	onNavItemClick,
	user,
	onSettings,
	onWorkspaceDelete,
	onManageMembers,
	onUserSettings,
	notifications,
	onLogout,
	theme,
	setTheme,
}: MobileSidebarProps) {
	const t = useTranslations("sidebar");
	const activeWorkspace = workspaces.find((w) => w.id === activeWorkspaceId);
	const [notificationsOpen, setNotificationsOpen] = useState(false);
	const close = () => onOpenChange(false);
	const addWorkspaceLabel =
		isAtWorkspaceLimit && maxWorkspacesPerUser !== undefined
			? t("workspace_limit_reached", { max: maxWorkspacesPerUser })
			: t("add_workspace");

	return (
		<Sheet open={isOpen} onOpenChange={onOpenChange}>
			<SheetContent
				side="left"
				className="flex w-[min(18rem,85vw)] flex-col gap-0 bg-panel p-0 [&>button]:hidden"
			>
				<SheetTitle className="sr-only">{t("navigation")}</SheetTitle>

				<div className="flex items-center gap-1 border-b px-2 py-2">
					<div className="flex min-w-0 flex-1 flex-col">
						{navItems.map((item) => {
							const Icon = item.icon;
							return (
								<button
									key={item.url}
									type="button"
									aria-current={item.isActive ? "page" : undefined}
									onClick={() => {
										onNavItemClick?.(item);
										close();
									}}
									className={cn(rowClassName, item.isActive && "bg-accent text-accent-foreground")}
								>
									<Icon className="h-5 w-5 text-muted-foreground" />
									<span className="flex-1 font-medium">{item.title}</span>
								</button>
							);
						})}
					</div>
					{notifications ? (
						<NotificationsDropdown
							notifications={notifications}
							onCloseMobileSidebar={close}
							open={notificationsOpen}
							onOpenChange={setNotificationsOpen}
						/>
					) : null}
				</div>

				<ScrollArea className="min-h-0 flex-1">
					<div className="px-2 py-3">
						<p className="px-2 pb-2 text-xs font-medium text-muted-foreground">{t("workspaces")}</p>
						<ul className="flex flex-col gap-1">
							{workspaces.map((space) => {
								const isActive = space.id === activeWorkspaceId;
								return (
									<li key={space.id} className="flex items-center gap-3 rounded-lg px-2">
										<WorkspaceAvatar
											name={space.name}
											isActive={isActive}
											isShared={space.memberCount > 1}
											isOwner={space.isOwner}
											onClick={() => {
												onWorkspaceSelect(space.id);
												close();
											}}
											size="sm"
											disableTooltip
										/>
										<button
											type="button"
											tabIndex={-1}
											aria-hidden
											onClick={() => {
												onWorkspaceSelect(space.id);
												close();
											}}
											className={cn(
												"min-w-0 flex-1 truncate py-2 text-left text-sm",
												isActive && "font-semibold"
											)}
										>
											{space.name}
										</button>
									</li>
								);
							})}
							<li>
								<button
									type="button"
									onClick={onAddWorkspace}
									disabled={isAtWorkspaceLimit}
									className={cn(rowClassName, "text-muted-foreground disabled:opacity-50")}
								>
									<span className="flex h-8 w-8 items-center justify-center rounded-lg border-2 border-dashed border-muted-foreground/30">
										<Plus className="h-4 w-4" />
									</span>
									<span className="flex-1">{addWorkspaceLabel}</span>
								</button>
							</li>
						</ul>
					</div>
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
					onBeforeAction={close}
					onUserSettings={onUserSettings}
					onNotifications={notifications ? () => setNotificationsOpen(true) : undefined}
					notificationsUnreadCount={notifications?.totalUnreadCount}
					onLogout={onLogout}
					theme={theme}
					setTheme={setTheme}
				/>
			</SheetContent>
		</Sheet>
	);
}
