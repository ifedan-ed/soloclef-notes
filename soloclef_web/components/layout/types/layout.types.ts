import type { LucideIcon } from "lucide-react";
import type { DocumentsProcessingStatus } from "@/hooks/use-documents-processing";

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
	navItems?: NavItem[];
	className?: string;
}

export interface SidebarUserProfileProps {
	user: User;
	onSettings?: () => void;
	onToggleTheme?: () => void;
	onLogout?: () => void;
	theme?: string;
}
