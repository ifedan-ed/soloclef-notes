"use client";

import { useAtomValue } from "jotai";
import { AlarmClock, BookText, Cpu, Earth, Settings, Unplug, UserKey } from "lucide-react";
import { useRouter, useSelectedLayoutSegment } from "next/navigation";
import { useTranslations } from "next-intl";
import type React from "react";
import { useEffect, useMemo } from "react";
import { myAccessAtom } from "@/atoms/members/members-query.atoms";
import { type RoutedSectionItem, RoutedSectionShell } from "@/components/layout";
import {
	audienceOf,
	leavesTab,
	showsWorkspaceSettingsTab,
} from "@/features/settings-visibility/settings-visibility";

export type WorkspaceSettingsTab =
	| "general"
	| "models"
	| "team-roles"
	| "prompts"
	| "public-links"
	| "connectors"
	| "automations";

// Master-detail pages with their own lists; a form-width column cramps them.
const WIDE_TABS: readonly WorkspaceSettingsTab[] = ["connectors", "automations"];

const DEFAULT_TAB: WorkspaceSettingsTab = "general";

interface WorkspaceSettingsLayoutShellProps {
	workspaceId: string;
	children: React.ReactNode;
}

export function WorkspaceSettingsLayoutShell({
	workspaceId,
	children,
}: WorkspaceSettingsLayoutShellProps) {
	const t = useTranslations("workspaceSettings");
	const segment = useSelectedLayoutSegment();
	const router = useRouter();
	const access = useAtomValue(myAccessAtom);
	const who = audienceOf(access.isError ? false : access.data?.is_owner);

	const navItems = useMemo<RoutedSectionItem[]>(
		() =>
			[
				{
					value: "general" as const,
					label: t("nav_general"),
					href: `/dashboard/${workspaceId}/workspace-settings/general`,
					icon: <Settings className="h-4 w-4" />,
				},
				{
					value: "connectors" as const,
					label: t("nav_connectors"),
					href: `/dashboard/${workspaceId}/workspace-settings/connectors`,
					icon: <Unplug className="h-4 w-4" />,
				},
				{
					value: "automations" as const,
					label: t("nav_automations"),
					href: `/dashboard/${workspaceId}/workspace-settings/automations`,
					icon: <AlarmClock className="h-4 w-4" />,
				},
				{
					value: "models" as const,
					label: t("nav_models"),
					href: `/dashboard/${workspaceId}/workspace-settings/models`,
					icon: <Cpu className="h-4 w-4" />,
				},
				{
					value: "team-roles" as const,
					label: t("nav_team_roles"),
					href: `/dashboard/${workspaceId}/workspace-settings/team-roles`,
					icon: <UserKey className="h-4 w-4" />,
				},
				{
					value: "prompts" as const,
					label: t("nav_system_instructions"),
					href: `/dashboard/${workspaceId}/workspace-settings/prompts`,
					icon: <BookText className="h-4 w-4" />,
				},
				{
					value: "public-links" as const,
					label: t("nav_public_links"),
					href: `/dashboard/${workspaceId}/workspace-settings/public-links`,
					icon: <Earth className="h-4 w-4" />,
				},
			].filter((item) => showsWorkspaceSettingsTab(item.value, who)),
		[t, workspaceId, who]
	);

	// An address typed in for a tab this person cannot see goes to General.
	const leaving = segment !== null && leavesTab(segment, who, showsWorkspaceSettingsTab);
	const waiting = segment !== null && !showsWorkspaceSettingsTab(segment, who) && !leaving;
	useEffect(() => {
		if (leaving) router.replace(`/dashboard/${workspaceId}/workspace-settings/${DEFAULT_TAB}`);
	}, [leaving, router, workspaceId]);

	const activeTab: WorkspaceSettingsTab =
		segment && navItems.some((item) => item.value === segment)
			? (segment as WorkspaceSettingsTab)
			: DEFAULT_TAB;
	const selectedLabel = navItems.find((item) => item.value === activeTab)?.label ?? t("title");

	return (
		<RoutedSectionShell
			title={t("title")}
			items={navItems}
			activeValue={activeTab}
			selectedLabel={selectedLabel}
			contentClassName={WIDE_TABS.includes(activeTab) ? "md:max-w-none" : "md:max-w-3xl"}
		>
			{leaving || waiting ? null : children}
		</RoutedSectionShell>
	);
}
