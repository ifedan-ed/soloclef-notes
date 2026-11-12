"use client";

import {
	AlarmClock,
	BookText,
	Brain,
	CircleUser,
	Cpu,
	Earth,
	Keyboard,
	KeyRound,
	Library,
	Megaphone,
	Monitor,
	Palette,
	ShieldCheck,
	Unplug,
	WandSparkles,
} from "lucide-react";
import { useRouter, useSelectedLayoutSegment } from "next/navigation";
import { useTranslations } from "next-intl";
import type React from "react";
import { useEffect, useMemo } from "react";
import { type RoutedSectionItem, RoutedSectionShell } from "@/components/layout";
import {
	audienceOf,
	leavesTab,
	showsSettingsTab,
} from "@/features/settings-visibility/settings-visibility";
import { usePlatform } from "@/hooks/use-platform";
import { useSiteAdmin } from "@/hooks/use-site-admin";
import { settingsPath } from "@/lib/dashboard-paths";

export type SettingsTab =
	| "profile"
	| "appearance"
	| "api-key"
	| "models"
	| "instructions"
	| "memory"
	| "prompts"
	| "connectors"
	| "automations"
	| "public-links"
	| "agent-permissions"
	| "announcements"
	| "community-prompts"
	| "desktop"
	| "hotkeys";

const DEFAULT_TAB: SettingsTab = "profile";

// Master-detail pages with lists of their own; a form-width column cramps them.
const WIDE_TABS: readonly SettingsTab[] = ["connectors", "automations"];

/**
 * The account's one settings page. Everything here is the account's own:
 * there is nothing shared to configure and nobody else to configure it for,
 * so the only sections held back are the administrators' (who sees which is
 * settings-visibility's decision).
 */
export function SettingsLayoutShell({ children }: { children: React.ReactNode }) {
	const t = useTranslations("settings");
	const { isDesktop } = usePlatform();
	const segment = useSelectedLayoutSegment();
	const router = useRouter();
	const who = audienceOf(useSiteAdmin().isAdmin);

	const navItems = useMemo<RoutedSectionItem[]>(() => {
		const item = (value: SettingsTab, label: string, icon: React.ReactNode) => ({
			value,
			label,
			href: settingsPath(value),
			icon,
		});
		return [
			item("profile", t("nav_profile"), <CircleUser className="h-4 w-4" />),
			item("appearance", t("nav_appearance"), <Palette className="h-4 w-4" />),
			item("api-key", t("nav_api_key"), <KeyRound className="h-4 w-4" />),
			item("models", t("nav_models"), <Cpu className="h-4 w-4" />),
			item("instructions", t("nav_instructions"), <BookText className="h-4 w-4" />),
			item("memory", t("nav_memory"), <Brain className="h-4 w-4" />),
			item("prompts", t("nav_prompts"), <WandSparkles className="h-4 w-4" />),
			item("connectors", t("nav_connectors"), <Unplug className="h-4 w-4" />),
			item("automations", t("nav_automations"), <AlarmClock className="h-4 w-4" />),
			item("public-links", t("nav_public_links"), <Earth className="h-4 w-4" />),
			item("agent-permissions", t("nav_agent_permissions"), <ShieldCheck className="h-4 w-4" />),
			item("announcements", t("nav_announcements"), <Megaphone className="h-4 w-4" />),
			item("community-prompts", t("nav_community_prompts"), <Library className="h-4 w-4" />),
			...(isDesktop
				? [
						item("desktop", t("nav_desktop"), <Monitor className="h-4 w-4" />),
						item("hotkeys", t("nav_hotkeys"), <Keyboard className="h-4 w-4" />),
					]
				: []),
		].filter((entry) => showsSettingsTab(entry.value, who));
	}, [t, isDesktop, who]);

	// A section this person cannot see, typed into the address bar, goes to
	// the first one; an administrator's waits until we know who is looking.
	const leaving = segment !== null && leavesTab(segment, who, showsSettingsTab);
	const waiting = segment !== null && !showsSettingsTab(segment, who) && !leaving;
	useEffect(() => {
		if (leaving) router.replace(settingsPath(DEFAULT_TAB));
	}, [leaving, router]);

	const activeTab: SettingsTab =
		segment && navItems.some((entry) => entry.value === segment)
			? (segment as SettingsTab)
			: DEFAULT_TAB;
	const selectedLabel = navItems.find((entry) => entry.value === activeTab)?.label ?? t("title");

	return (
		<RoutedSectionShell
			title={t("title")}
			items={navItems}
			activeValue={activeTab}
			selectedLabel={selectedLabel}
			mobileNav="drawer"
			contentClassName={WIDE_TABS.includes(activeTab) ? "md:max-w-none" : "md:max-w-3xl"}
		>
			{leaving || waiting ? null : children}
		</RoutedSectionShell>
	);
}
