import type { LucideIcon } from "lucide-react";
import type { DocumentsProcessingStatus } from "@/hooks/use-documents-processing";

export interface Workspace {
	id: number;
	name: string;
	description?: string | null;
	isOwner: boolean;
	memberCount: number;
	createdAt?: string;
}

export interface User {
	email: string;
	name?: string;
	avatarUrl?: string;
}

export interface NavItem {
	title: string;
	url: string;
	icon: LucideIcon;
	isActive?: boolean;
	badge?: string | number;
	statusIndicator?: DocumentsProcessingStatus;
}

export interface PageUsage {
	pagesUsed: number;
	pagesLimit: number;
}

export interface IconRailProps {
	workspaces: Workspace[];
	activeWorkspaceId: number | null;
	onWorkspaceSelect: (id: number) => void;
	onAddWorkspace: () => void;
	className?: string;
}

export interface SidebarUserProfileProps {
	user: User;
	workspaceId?: string;
	onSettings?: () => void;
	onManageMembers?: () => void;
	onSwitchWorkspace?: () => void;
	onToggleTheme?: () => void;
	onLogout?: () => void;
	theme?: string;
}
