"use client";

import { PanelLeft } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import { cn } from "@/lib/utils";
import type { NavItem, User } from "../../types/layout.types";
import { NotificationsDropdown, type NotificationsDropdownData } from "./NotificationsDropdown";
import { SidebarUserProfile } from "./SidebarUserProfile";

interface MobileSidebarProps {
	isOpen: boolean;
	onOpenChange: (open: boolean) => void;
	navItems: NavItem[];
	onNavItemClick?: (item: NavItem) => void;
	user: User;
	/** Opens the account's settings, from the account menu. */
	onSettings?: () => void;
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
 * (Home, Notes), with the bell and the account menu.
 */
export function MobileSidebar({
	isOpen,
	onOpenChange,
	navItems,
	onNavItemClick,
	user,
	onSettings,
	notifications,
	onLogout,
	theme,
	setTheme,
}: MobileSidebarProps) {
	const t = useTranslations("sidebar");
	const [notificationsOpen, setNotificationsOpen] = useState(false);
	const close = () => onOpenChange(false);

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

				<div className="min-h-0 flex-1" />
				<SidebarUserProfile
					user={user}
					showConnectAgent
					onBeforeAction={close}
					onSettings={onSettings}
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
