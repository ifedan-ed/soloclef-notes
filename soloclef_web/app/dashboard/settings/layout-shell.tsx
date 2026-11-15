"use client";

import {
	AlarmClock,
	Bell,
	BookText,
	Brain,
	CircleUser,
	Cpu,
	Earth,
	KeyRound,
	LockKeyhole,
	Palette,
	ShieldCheck,
	Unplug,
	WandSparkles,
} from "lucide-react";
import { useSelectedLayoutSegment } from "next/navigation";
import { useTranslations } from "next-intl";
import type React from "react";
import { useMemo } from "react";
import { type RoutedSectionItem, RoutedSectionShell } from "@/components/layout";
import { settingsPath } from "@/lib/dashboard-paths";

export type SettingsTab =
	| "profile"
	| "security"
	| "appearance"
	| "notifications"
	| "api-key"
	| "models"
	| "instructions"
	| "memory"
	| "prompts"
	| "connectors"
	| "automations"
	| "public-links"
	| "agent-permissions";

const DEFAULT_TAB: SettingsTab = "profile";

// Master-detail pages with lists of their own; a form-width column cramps them.
const WIDE_TABS: readonly SettingsTab[] = ["connectors", "automations"];

/**
 * The account's one settings page. Everything here is the account's own:
 * there is nothing shared to configure and nobody else to configure it for,
 * so every account sees every section.
 */
export function SettingsLayoutShell({ children }: { children: React.ReactNode }) {
	const t = useTranslations("settings");
	const segment = useSelectedLayoutSegment();

	const navItems = useMemo<RoutedSectionItem[]>(() => {
		const item = (value: SettingsTab, label: string, icon: React.ReactNode) => ({
			value,
			label,
			href: settingsPath(value),
			icon,
		});
		return [
			item("profile", t("nav_profile"), <CircleUser className="h-4 w-4" />),
			item("security", t("nav_security"), <LockKeyhole className="h-4 w-4" />),
			item("appearance", t("nav_appearance"), <Palette className="h-4 w-4" />),
			item("notifications", t("nav_notifications"), <Bell className="h-4 w-4" />),
			item("api-key", t("nav_api_key"), <KeyRound className="h-4 w-4" />),
			item("models", t("nav_models"), <Cpu className="h-4 w-4" />),
			item("instructions", t("nav_instructions"), <BookText className="h-4 w-4" />),
			item("memory", t("nav_memory"), <Brain className="h-4 w-4" />),
			item("prompts", t("nav_prompts"), <WandSparkles className="h-4 w-4" />),
			item("connectors", t("nav_connectors"), <Unplug className="h-4 w-4" />),
			item("automations", t("nav_automations"), <AlarmClock className="h-4 w-4" />),
			item("public-links", t("nav_public_links"), <Earth className="h-4 w-4" />),
			item("agent-permissions", t("nav_agent_permissions"), <ShieldCheck className="h-4 w-4" />),
		];
	}, [t]);

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
			{children}
		</RoutedSectionShell>
	);
}
