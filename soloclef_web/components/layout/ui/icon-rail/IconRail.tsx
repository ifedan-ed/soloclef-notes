"use client";

import { useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import type { NavItem, User } from "../../types/layout.types";
import {
	NotificationsDropdown,
	type NotificationsDropdownData,
} from "../sidebar/NotificationsDropdown";
import { SidebarUserProfile } from "../sidebar/SidebarUserProfile";

interface IconRailProps {
	/** Page links shown at the top of the rail (Home, Notes). */
	navItems?: NavItem[];
	onNavItemClick?: (item: NavItem) => void;
	user: User;
	/** Opens the account's settings, from the account menu. */
	onSettings?: () => void;
	notifications?: NotificationsDropdownData;
	onLogout?: () => void;
	theme?: string;
	setTheme?: (theme: "light" | "dark" | "system") => void;
	className?: string;
}

/**
 * The app's only sidebar: Home and Notes at the top, then the bell and the
 * account menu at the foot. The account menu holds settings and "Connect
 * your agent".
 */
export function IconRail({
	navItems = [],
	onNavItemClick,
	user,
	onSettings,
	notifications,
	onLogout,
	theme,
	setTheme,
	className,
}: IconRailProps) {
	const t = useTranslations("sidebar");
	// The bell's panel, also opened from "Notifications" in the account menu.
	const [notificationsOpen, setNotificationsOpen] = useState(false);

	return (
		<nav
			aria-label={t("navigation")}
			className={cn("flex h-full w-14 min-h-0 flex-col items-center", className)}
		>
			<div className="flex w-full min-h-0 flex-1 flex-col items-center gap-2 px-1.5 pt-3">
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
			</div>
			<SidebarUserProfile
				user={user}
				showConnectAgent
				onSettings={onSettings}
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
