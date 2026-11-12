"use client";

import { useSetAtom } from "jotai";
import {
	Bell,
	Check,
	ChevronRight,
	ChevronUp,
	ExternalLink,
	FolderSync,
	Info,
	Languages,
	LogOut,
	Monitor,
	Moon,
	Settings,
	Sun,
} from "lucide-react";
import Image from "next/image";
import { useTranslations } from "next-intl";
import type React from "react";
import { Fragment, useRef, useState } from "react";
import { watchedFoldersRefreshAtom } from "@/atoms/documents/folder.atoms";
import { ConnectAgentDialog, McpIcon } from "@/components/mcp/connect-agent-dialog";
import { FolderWatchDialog } from "@/components/sources/FolderWatchDialog";
import { Button } from "@/components/ui/button";
import { Drawer, DrawerContent, DrawerHandle, DrawerTitle } from "@/components/ui/drawer";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuPortal,
	DropdownMenuSeparator,
	DropdownMenuSub,
	DropdownMenuSubContent,
	DropdownMenuSubTrigger,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Spinner } from "@/components/ui/spinner";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useLocaleContext } from "@/contexts/LocaleContext";
import { useMediaQuery } from "@/hooks/use-media-query";
import { usePlatform } from "@/hooks/use-platform";
import { APP_VERSION } from "@/lib/env-config";
import { getUserAvatarColor, getUserInitials } from "@/lib/user-avatar";
import { cn } from "@/lib/utils";
import type { User } from "../../types/layout.types";
import { SidebarButtonBadge } from "./SidebarButton";

// Supported languages configuration
const LANGUAGES = [
	{ code: "en" as const, name: "English", flag: "🇺🇸" },
	{ code: "es" as const, name: "Español", flag: "🇪🇸" },
	{ code: "pt" as const, name: "Português", flag: "🇧🇷" },
	{ code: "hi" as const, name: "हिन्दी", flag: "🇮🇳" },
	{ code: "zh" as const, name: "简体中文", flag: "🇨🇳" },
	{ code: "ko" as const, name: "한국어", flag: "🇰🇷" },
];

// Supported themes configuration
const THEMES = [
	{ value: "light" as const, name: "Light", icon: Sun },
	{ value: "dark" as const, name: "Dark", icon: Moon },
	{ value: "system" as const, name: "System", icon: Monitor },
];

const LEARN_MORE_LINKS = [{ key: "documentation" as const, href: "/docs" }];

type MobileProfileSubmenu = "theme" | "language" | "learn_more";

interface SidebarUserProfileProps {
	user: User;
	/** Adds "Connect your agent" (the MCP setup guide) to the menu. */
	showConnectAgent?: boolean;
	/** Called before a menu action runs (the mobile sheet closes itself). */
	onBeforeAction?: () => void;
	/** Opens the account's settings. */
	onSettings?: () => void;
	/** Opens the notifications panel (the bell's). */
	onNotifications?: () => void;
	notificationsUnreadCount?: number;
	onLogout?: () => void;
	isCollapsed?: boolean;
	theme?: string;
	setTheme?: (theme: "light" | "dark" | "system") => void;
	topContent?: React.ReactNode;
}

function formatUnreadCount(count: number): string {
	if (count <= 999) {
		return count.toString();
	}
	const thousands = Math.floor(count / 1000);
	return `${thousands}k+`;
}

/**
 * User avatar component - shows image if available, otherwise falls back to initials
 */
function UserAvatar({
	avatarUrl,
	initials,
	bgColor,
	size = "sm",
}: {
	avatarUrl?: string;
	initials: string;
	bgColor: string;
	size?: "sm" | "md";
}) {
	const sizeClass = size === "md" ? "h-10 w-10" : "h-8 w-8";

	if (avatarUrl) {
		return (
			<Image
				src={avatarUrl}
				alt="User avatar"
				width={size === "md" ? 40 : 32}
				height={size === "md" ? 40 : 32}
				className={cn(sizeClass, "shrink-0 rounded-full object-cover select-none")}
				referrerPolicy="no-referrer"
				unoptimized
			/>
		);
	}

	return (
		<div
			className={cn(
				sizeClass,
				"flex shrink-0 items-center justify-center rounded-full text-xs font-semibold text-white select-none"
			)}
			style={{ backgroundColor: bgColor }}
		>
			{initials}
		</div>
	);
}

export function SidebarUserProfile({
	user,
	showConnectAgent = false,
	onBeforeAction,
	onSettings,
	onNotifications,
	notificationsUnreadCount = 0,
	onLogout,
	isCollapsed = false,
	theme,
	setTheme,
	topContent,
}: SidebarUserProfileProps) {
	const t = useTranslations("sidebar");
	const { locale, setLocale } = useLocaleContext();
	const isDesktopViewport = useMediaQuery("(min-width: 768px)");
	const [isLoggingOut, setIsLoggingOut] = useState(false);
	const [mobileSubmenu, setMobileSubmenu] = useState<MobileProfileSubmenu | null>(null);
	const bgColor = getUserAvatarColor(user.email);
	const initials = getUserInitials(user.email);
	const displayName = user.name || user.email.split("@")[0];
	const useMobileSubmenus = !isDesktopViewport;
	const [connectAgentOpen, setConnectAgentOpen] = useState(false);
	const [folderWatchOpen, setFolderWatchOpen] = useState(false);
	const { isDesktop } = usePlatform();
	const bumpWatchedFoldersRefresh = useSetAtom(watchedFoldersRefreshAtom);
	// A menu item's action runs once the menu has closed (onCloseAutoFocus), and
	// the menu then leaves focus alone: a dialog or panel the action opens keeps
	// it instead of losing it to the avatar and closing again.
	const pendingAction = useRef<(() => void) | null>(null);
	// keepSheetOpen: the action opens something this component renders (a
	// dialog, the bell's panel), which must outlive the mobile sheet closing.
	const runNow = (action?: () => void, keepSheetOpen = false) => {
		if (!keepSheetOpen) onBeforeAction?.();
		action?.();
	};
	const act =
		(action?: () => void, keepSheetOpen = false) =>
		() => {
			pendingAction.current = () => runNow(action, keepSheetOpen);
		};
	const runPendingAction = (event: Event) => {
		const action = pendingAction.current;
		if (!action) return;
		pendingAction.current = null;
		event.preventDefault();
		action();
	};

	const handleLanguageChange = (newLocale: "en" | "es" | "pt" | "hi" | "zh" | "ko") => {
		setLocale(newLocale);
	};

	const handleThemeChange = (newTheme: "light" | "dark" | "system") => {
		setTheme?.(newTheme);
	};

	const handleLogout = async () => {
		if (isLoggingOut || !onLogout) return;
		setIsLoggingOut(true);
		try {
			await onLogout();
		} finally {
			setIsLoggingOut(false);
		}
	};

	const submenuTriggerClassName = "cursor-default";
	const drawerItemClassName = cn(
		"flex h-12 w-full items-center gap-3 rounded-lg px-3 text-left text-sm transition-colors",
		"hover:bg-accent hover:text-accent-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
	);
	const drawerSeparatorClassName = "mx-3 h-px bg-popover-border";

	const renderAppItems = () => (
		<>
			{showConnectAgent && (
				<DropdownMenuItem onSelect={act(() => setConnectAgentOpen(true), true)}>
					<McpIcon className="size-4" />
					<span className="flex-1">{t("connect_agent")}</span>
					<SidebarButtonBadge>{t("new_badge")}</SidebarButtonBadge>
				</DropdownMenuItem>
			)}
			{isDesktop && (
				<DropdownMenuItem onSelect={act(() => setFolderWatchOpen(true), true)}>
					<FolderSync className="h-4 w-4" />
					{t("watch_local_folder")}
				</DropdownMenuItem>
			)}
		</>
	);

	const menuDialogs = (
		<>
			{showConnectAgent && (
				<ConnectAgentDialog open={connectAgentOpen} onOpenChange={setConnectAgentOpen} />
			)}
			{isDesktop ? (
				<FolderWatchDialog
					open={folderWatchOpen}
					onOpenChange={setFolderWatchOpen}
					onSuccess={() => bumpWatchedFoldersRefresh((c) => c + 1)}
				/>
			) : null}
		</>
	);

	const renderThemeSubmenu = () => {
		if (!setTheme) return null;

		if (useMobileSubmenus) {
			return (
				<DropdownMenuItem
					className={submenuTriggerClassName}
					onSelect={() => setMobileSubmenu("theme")}
				>
					<Sun className="h-4 w-4" />
					<span className="flex-1">{t("theme")}</span>
					<ChevronRight className="h-4 w-4 text-muted-foreground" />
				</DropdownMenuItem>
			);
		}

		return (
			<DropdownMenuSub>
				<DropdownMenuSubTrigger>
					<Sun className="h-4 w-4" />
					{t("theme")}
				</DropdownMenuSubTrigger>
				<DropdownMenuPortal>
					<DropdownMenuSubContent className="gap-1">
						{THEMES.map((themeOption) => {
							const Icon = themeOption.icon;
							const isSelected = theme === themeOption.value;
							return (
								<DropdownMenuItem
									key={themeOption.value}
									onClick={() => handleThemeChange(themeOption.value)}
									className={cn(
										"mb-1 last:mb-0 transition-all",
										"hover:bg-accent hover:text-accent-foreground",
										isSelected && "text-primary"
									)}
								>
									<Icon className="h-4 w-4" />
									<span className="flex-1">{t(themeOption.value)}</span>
									{isSelected && <Check className="h-4 w-4 shrink-0" />}
								</DropdownMenuItem>
							);
						})}
					</DropdownMenuSubContent>
				</DropdownMenuPortal>
			</DropdownMenuSub>
		);
	};

	const renderLanguageSubmenu = () => {
		if (useMobileSubmenus) {
			return (
				<DropdownMenuItem
					className={submenuTriggerClassName}
					onSelect={() => setMobileSubmenu("language")}
				>
					<Languages className="h-4 w-4" />
					<span className="flex-1">{t("language")}</span>
					<ChevronRight className="h-4 w-4 text-muted-foreground" />
				</DropdownMenuItem>
			);
		}

		return (
			<DropdownMenuSub>
				<DropdownMenuSubTrigger>
					<Languages className="h-4 w-4" />
					{t("language")}
				</DropdownMenuSubTrigger>
				<DropdownMenuPortal>
					<DropdownMenuSubContent className="gap-1">
						{LANGUAGES.map((language) => {
							const isSelected = locale === language.code;
							return (
								<DropdownMenuItem
									key={language.code}
									onClick={() => handleLanguageChange(language.code)}
									className={cn(
										"mb-1 last:mb-0 transition-all",
										"hover:bg-accent hover:text-accent-foreground",
										isSelected && "text-primary"
									)}
								>
									<span className="mr-2">{language.flag}</span>
									<span className="flex-1">{language.name}</span>
									{isSelected && <Check className="h-4 w-4 shrink-0" />}
								</DropdownMenuItem>
							);
						})}
					</DropdownMenuSubContent>
				</DropdownMenuPortal>
			</DropdownMenuSub>
		);
	};

	const renderLearnMoreSubmenu = () => {
		if (useMobileSubmenus) {
			return (
				<DropdownMenuItem
					className={submenuTriggerClassName}
					onSelect={() => setMobileSubmenu("learn_more")}
				>
					<Info className="h-4 w-4" />
					<span className="flex-1">{t("learn_more")}</span>
					<ChevronRight className="h-4 w-4 text-muted-foreground" />
				</DropdownMenuItem>
			);
		}

		return (
			<DropdownMenuSub>
				<DropdownMenuSubTrigger>
					<Info className="h-4 w-4" />
					{t("learn_more")}
				</DropdownMenuSubTrigger>
				<DropdownMenuPortal>
					<DropdownMenuSubContent className="min-w-[180px] gap-1">
						{LEARN_MORE_LINKS.map((link) => (
							<DropdownMenuItem key={link.key} asChild>
								<a href={link.href} target="_blank" rel="noopener noreferrer">
									<span className="flex-1">{t(link.key)}</span>
									<ExternalLink className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
								</a>
							</DropdownMenuItem>
						))}
						<DropdownMenuSeparator />
						<p className="select-none px-2 py-1 text-xs leading-tight text-muted-foreground/50">
							v{APP_VERSION}
						</p>
					</DropdownMenuSubContent>
				</DropdownMenuPortal>
			</DropdownMenuSub>
		);
	};

	const mobileSubmenuDrawer = (
		<Drawer
			open={mobileSubmenu !== null}
			onOpenChange={(open) => {
				if (!open) setMobileSubmenu(null);
			}}
			shouldScaleBackground={false}
		>
			<DrawerContent
				className="z-80 max-h-[75vh] rounded-t-2xl border bg-popover text-popover-foreground"
				overlayClassName="z-80"
			>
				<DrawerHandle className="mt-3 h-1.5 w-10" />
				<DrawerTitle className="px-4 pb-2 pt-3 text-center text-base font-semibold">
					{mobileSubmenu === "theme"
						? t("theme")
						: mobileSubmenu === "language"
							? t("language")
							: t("learn_more")}
				</DrawerTitle>
				<div className="space-y-1 px-4 pb-6 pt-1">
					{mobileSubmenu === "theme" &&
						setTheme &&
						THEMES.map((themeOption, index) => {
							const Icon = themeOption.icon;
							const isSelected = theme === themeOption.value;
							return (
								<Fragment key={themeOption.value}>
									{index > 0 && <div className={drawerSeparatorClassName} />}
									<button
										type="button"
										className={cn(drawerItemClassName, isSelected && "text-primary")}
										onClick={() => {
											handleThemeChange(themeOption.value);
											setMobileSubmenu(null);
										}}
									>
										<Icon className="h-4 w-4 text-muted-foreground" />
										<span className="flex-1">{t(themeOption.value)}</span>
										{isSelected && <Check className="h-4 w-4 shrink-0" />}
									</button>
								</Fragment>
							);
						})}

					{mobileSubmenu === "language" &&
						LANGUAGES.map((language, index) => {
							const isSelected = locale === language.code;
							return (
								<Fragment key={language.code}>
									{index > 0 && <div className={drawerSeparatorClassName} />}
									<button
										type="button"
										className={cn(drawerItemClassName, isSelected && "text-primary")}
										onClick={() => {
											handleLanguageChange(language.code);
											setMobileSubmenu(null);
										}}
									>
										<span className="text-base">{language.flag}</span>
										<span className="flex-1">{language.name}</span>
										{isSelected && <Check className="h-4 w-4 shrink-0" />}
									</button>
								</Fragment>
							);
						})}

					{mobileSubmenu === "learn_more" && (
						<>
							{LEARN_MORE_LINKS.map((link, index) => (
								<Fragment key={link.key}>
									{index > 0 && <div className={drawerSeparatorClassName} />}
									<a
										href={link.href}
										target="_blank"
										rel="noopener noreferrer"
										className={drawerItemClassName}
										onClick={() => setMobileSubmenu(null)}
									>
										<span className="flex-1">{t(link.key)}</span>
										<ExternalLink className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
									</a>
								</Fragment>
							))}
							<p className="select-none px-3 py-1 text-xs leading-tight text-muted-foreground/50">
								v{APP_VERSION}
							</p>
						</>
					)}
				</div>
			</DrawerContent>
		</Drawer>
	);

	const menuItems = (
		<>
			<DropdownMenuItem onSelect={act(onSettings)}>
				<Settings className="h-4 w-4" />
				{t("settings")}
			</DropdownMenuItem>

			{renderAppItems()}

			{onNotifications ? (
				<DropdownMenuItem onSelect={act(onNotifications, true)}>
					<Bell className="h-4 w-4" />
					<span className="flex-1">{t("notifications")}</span>
					{notificationsUnreadCount > 0 && (
						<span className="inline-flex items-center justify-center min-w-4 h-4 px-1 rounded-full bg-red-500 text-white text-[10px] font-medium">
							{formatUnreadCount(notificationsUnreadCount)}
						</span>
					)}
				</DropdownMenuItem>
			) : null}

			{renderThemeSubmenu()}

			{renderLanguageSubmenu()}

			{renderLearnMoreSubmenu()}

			<DropdownMenuSeparator />

			<DropdownMenuItem onClick={handleLogout} disabled={isLoggingOut}>
				{isLoggingOut ? <Spinner size="sm" className="mr-2" /> : <LogOut className="h-4 w-4" />}
				{isLoggingOut ? t("loggingOut") : t("logout")}
			</DropdownMenuItem>
		</>
	);

	// Collapsed view (the rail) - just the avatar, which opens the account menu
	if (isCollapsed) {
		return (
			<div className="relative w-full px-1.5 py-2 before:absolute before:inset-x-1.5 before:top-0 before:h-px before:bg-border">
				<div className="flex flex-col items-center gap-2">
					{topContent}
					<DropdownMenu>
						<Tooltip>
							<TooltipTrigger asChild>
								<DropdownMenuTrigger asChild>
									<Button
										type="button"
										variant="ghost"
										aria-label={t("account_menu", { name: displayName })}
										className={cn(
											"h-10 w-10 rounded-full p-0",
											"transition-opacity hover:bg-transparent hover:opacity-90",
											"focus:outline-none focus-visible:ring-2 focus-visible:ring-ring",
											"data-[state=open]:opacity-90"
										)}
									>
										<UserAvatar
											avatarUrl={user.avatarUrl}
											initials={initials}
											bgColor={bgColor}
											size="md"
										/>
									</Button>
								</DropdownMenuTrigger>
							</TooltipTrigger>
							<TooltipContent side="right" sideOffset={8}>
								{t("account_menu", { name: displayName })}
							</TooltipContent>
						</Tooltip>

						<DropdownMenuContent
							className="w-60"
							side="right"
							align="end"
							sideOffset={8}
							onCloseAutoFocus={runPendingAction}
						>
							<DropdownMenuLabel className="px-2 py-1 font-normal">
								<div className="min-w-0">
									{/* <p className="truncate text-sm font-medium">{displayName}</p> */}
									<p className="truncate text-xs font-semibold leading-tight text-muted-foreground">
										{user.email}
									</p>
								</div>
							</DropdownMenuLabel>

							{menuItems}
						</DropdownMenuContent>
					</DropdownMenu>
					{mobileSubmenuDrawer}
					{menuDialogs}
				</div>
			</div>
		);
	}

	// Expanded view
	return (
		<div className="border-t">
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<Button
						type="button"
						variant="ghost"
						className={cn(
							"h-auto w-full justify-start gap-2 rounded-none px-2 py-3 text-left",
							"hover:bg-accent transition-colors",
							"focus:outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
							"data-[state=open]:bg-transparent"
						)}
					>
						<UserAvatar avatarUrl={user.avatarUrl} initials={initials} bgColor={bgColor} />

						{/* Name and email */}
						<div className="flex-1 min-w-0">
							<p className="truncate text-sm font-medium">{displayName}</p>
							<p className="truncate text-xs text-muted-foreground">{user.email}</p>
						</div>

						{/* Chevron icon */}
						<ChevronUp className="h-4 w-4 shrink-0 text-muted-foreground" />
					</Button>
				</DropdownMenuTrigger>

				<DropdownMenuContent
					className="w-60"
					side="top"
					align="center"
					sideOffset={4}
					onCloseAutoFocus={runPendingAction}
				>
					<DropdownMenuLabel className="px-2 py-1 font-normal">
						<div className="min-w-0">
							<p className="truncate text-sm font-medium">{displayName}</p>
							<p className="truncate text-xs font-semibold leading-tight text-muted-foreground">
								{user.email}
							</p>
						</div>
					</DropdownMenuLabel>

					{menuItems}
				</DropdownMenuContent>
			</DropdownMenu>
			{mobileSubmenuDrawer}
			{menuDialogs}
		</div>
	);
}
