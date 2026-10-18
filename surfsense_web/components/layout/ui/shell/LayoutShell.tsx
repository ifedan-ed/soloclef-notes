"use client";

import dynamic from "next/dynamic";
import { useState } from "react";
import { TooltipProvider } from "@/components/ui/tooltip";
import { useIsMobile } from "@/hooks/use-mobile";
import { useElectronAPI } from "@/hooks/use-platform";
import { cn } from "@/lib/utils";
import type { NavItem, User, Workspace } from "../../types/layout.types";
import { Header } from "../header";
import { IconRail } from "../icon-rail";
import { MobileSidebar, MobileSidebarTrigger } from "../sidebar";
import type { NotificationsDropdownData } from "../sidebar/NotificationsDropdown";
import { DESKTOP_LAYOUT_COLUMNS } from "./desktop-layout-columns";
import { FocusShell } from "./FocusShell";
import { WorkspacePanel } from "./WorkspacePanel";

const MobileArtifactDrawer = dynamic(
	() =>
		import("@/features/artifacts/ui/artifact-panel").then((m) => ({
			default: m.MobileArtifactDrawer,
		})),
	{ ssr: false }
);
const MobileDocumentViewerPanel = dynamic(
	() =>
		import("@/features/documents/viewer/document-viewer-panel").then((module) => ({
			default: module.MobileDocumentViewerPanel,
		})),
	{ ssr: false }
);

/** The macOS desktop app's draggable title bar (room for the window buttons). */
function MacDesktopTitleBar() {
	return <div className="h-9 shrink-0 bg-rail [app-region:drag] [-webkit-app-region:drag]" />;
}

interface LayoutShellProps {
	workspaces: Workspace[];
	activeWorkspaceId: number | null;
	onWorkspaceSelect: (id: number) => void;
	onWorkspaceDelete?: (workspace: Workspace) => void;
	onWorkspaceSettings?: (workspace: Workspace) => void;
	onAddWorkspace: () => void;
	isAtWorkspaceLimit?: boolean;
	maxWorkspacesPerUser?: number;
	/** Page links at the top of the rail (Home). */
	navItems: NavItem[];
	onNavItemClick?: (item: NavItem) => void;
	user: User;
	onSettings?: () => void;
	onManageMembers?: () => void;
	onUserSettings?: () => void;
	onLogout?: () => void;
	theme?: string;
	setTheme?: (theme: "light" | "dark" | "system") => void;
	workspacePanelViewportClassName?: string;
	workspacePanelContentClassName?: string;
	children: React.ReactNode;
	className?: string;
	notifications?: NotificationsDropdownData;
	/** Full-screen page without the app sidebars (a notebook). */
	focusMode?: boolean;
}

export function LayoutShell({
	workspaces,
	activeWorkspaceId,
	onWorkspaceSelect,
	onWorkspaceDelete,
	onWorkspaceSettings,
	onAddWorkspace,
	isAtWorkspaceLimit = false,
	maxWorkspacesPerUser,
	navItems,
	onNavItemClick,
	user,
	onSettings,
	onManageMembers,
	onUserSettings,
	onLogout,
	theme,
	setTheme,
	workspacePanelViewportClassName,
	workspacePanelContentClassName,
	children,
	className,
	notifications,
	focusMode = false,
}: LayoutShellProps) {
	const isMobile = useIsMobile();
	const electronAPI = useElectronAPI();
	const isMacDesktop = electronAPI?.versions.platform === "darwin";
	const [mobileMenuOpen, setMobileMenuOpen] = useState(false);

	if (focusMode) {
		return (
			<TooltipProvider delayDuration={0}>
				<FocusShell
					className={className}
					footer={
						<>
							<MobileArtifactDrawer />
							<MobileDocumentViewerPanel />
						</>
					}
				>
					{children}
				</FocusShell>
			</TooltipProvider>
		);
	}

	// Mobile layout
	if (isMobile) {
		return (
			<TooltipProvider delayDuration={0}>
				<div className={cn("flex h-dvh w-full flex-col bg-panel", className)}>
					<Header
						mobileMenuTrigger={<MobileSidebarTrigger onClick={() => setMobileMenuOpen(true)} />}
					/>

					<MobileSidebar
						isOpen={mobileMenuOpen}
						onOpenChange={setMobileMenuOpen}
						workspaces={workspaces}
						activeWorkspaceId={activeWorkspaceId}
						onWorkspaceSelect={onWorkspaceSelect}
						onAddWorkspace={onAddWorkspace}
						isAtWorkspaceLimit={isAtWorkspaceLimit}
						maxWorkspacesPerUser={maxWorkspacesPerUser}
						navItems={navItems}
						onNavItemClick={onNavItemClick}
						user={user}
						onSettings={onSettings}
						onWorkspaceDelete={onWorkspaceDelete}
						onManageMembers={onManageMembers}
						onUserSettings={onUserSettings}
						notifications={notifications}
						onLogout={onLogout}
						theme={theme}
						setTheme={setTheme}
					/>

					<WorkspacePanel
						viewportClassName={workspacePanelViewportClassName}
						contentClassName={workspacePanelContentClassName}
					>
						{children}
					</WorkspacePanel>
					<MobileArtifactDrawer />
					<MobileDocumentViewerPanel />
				</div>
			</TooltipProvider>
		);
	}

	// Desktop layout: the rail, then the page. There is no second sidebar:
	// Home sits at the top of the rail and the rest in the account menu.
	return (
		<TooltipProvider delayDuration={0}>
			<div className={cn("flex h-dvh w-full flex-col overflow-hidden bg-rail", className)}>
				{isMacDesktop ? <MacDesktopTitleBar /> : null}
				<div
					data-desktop-layout
					className={cn("grid min-h-0 w-full flex-1 overflow-hidden pl-2", DESKTOP_LAYOUT_COLUMNS)}
				>
					<div
						className={cn("hidden overflow-hidden bg-rail md:flex", !isMacDesktop && "border-r")}
					>
						<IconRail
							workspaces={workspaces}
							activeWorkspaceId={activeWorkspaceId}
							onWorkspaceSelect={onWorkspaceSelect}
							onWorkspaceDelete={onWorkspaceDelete}
							onWorkspaceSettings={onWorkspaceSettings}
							onAddWorkspace={onAddWorkspace}
							isAtWorkspaceLimit={isAtWorkspaceLimit}
							maxWorkspacesPerUser={maxWorkspacesPerUser}
							navItems={navItems}
							onNavItemClick={onNavItemClick}
							user={user}
							onSettings={onSettings}
							onManageMembers={onManageMembers}
							onUserSettings={onUserSettings}
							notifications={notifications}
							onLogout={onLogout}
							theme={theme}
							setTheme={setTheme}
						/>
					</div>

					<WorkspacePanel
						className={isMacDesktop ? "rounded-tl-xl border-l border-t" : undefined}
						viewportClassName={workspacePanelViewportClassName}
						contentClassName={workspacePanelContentClassName}
					>
						{children}
					</WorkspacePanel>
				</div>
				<MobileArtifactDrawer />
				<MobileDocumentViewerPanel />
			</div>
		</TooltipProvider>
	);
}
