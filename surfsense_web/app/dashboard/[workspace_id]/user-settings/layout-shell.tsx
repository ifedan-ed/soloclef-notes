"use client";

import {
	CircleUser,
	CreditCard,
	Keyboard,
	KeyRound,
	Library,
	Megaphone,
	Monitor,
	Palette,
	ShieldCheck,
	SlidersHorizontal,
	WandSparkles,
} from "lucide-react";
import { useRouter, useSelectedLayoutSegment } from "next/navigation";
import { useTranslations } from "next-intl";
import type React from "react";
import { useEffect, useMemo } from "react";
import { type RoutedSectionItem, RoutedSectionShell } from "@/components/layout";
import { useBillingMe } from "@/features/billing/hooks/use-billing-queries";
import {
	audienceOf,
	leavesTab,
	showsUserSettingsTab,
} from "@/features/settings-visibility/settings-visibility";
import { usePlatform } from "@/hooks/use-platform";

export type UserSettingsTab =
	| "profile"
	| "appearance"
	| "api-key"
	| "billing"
	| "billing-admin"
	| "announcements"
	| "prompts"
	| "community-prompts"
	| "agent-permissions"
	| "desktop"
	| "hotkeys";

const DEFAULT_TAB: UserSettingsTab = "profile";

interface UserSettingsLayoutShellProps {
	workspaceId: string;
	children: React.ReactNode;
}

export function UserSettingsLayoutShell({ workspaceId, children }: UserSettingsLayoutShellProps) {
	const t = useTranslations("userSettings");
	const { isDesktop } = usePlatform();
	const segment = useSelectedLayoutSegment();
	const tBilling = useTranslations("billing");
	const router = useRouter();
	const billingMe = useBillingMe();
	const isBillingAdmin = billingMe.data?.is_admin === true;
	const who = audienceOf(billingMe.isError ? false : billingMe.data?.is_admin);

	const navItems = useMemo<RoutedSectionItem[]>(
		() =>
			[
				{
					value: "profile" as const,
					label: t("profile_nav_label"),
					href: `/dashboard/${workspaceId}/user-settings/profile`,
					icon: <CircleUser className="h-4 w-4" />,
				},
				{
					value: "appearance" as const,
					label: "Appearance",
					href: `/dashboard/${workspaceId}/user-settings/appearance`,
					icon: <Palette className="h-4 w-4" />,
				},
				{
					value: "api-key" as const,
					label: t("api_key_nav_label"),
					href: `/dashboard/${workspaceId}/user-settings/api-key`,
					icon: <KeyRound className="h-4 w-4" />,
				},
				{
					value: "billing" as const,
					label: tBilling("nav_label"),
					href: `/dashboard/${workspaceId}/user-settings/billing`,
					icon: <CreditCard className="h-4 w-4" />,
				},
				...(isBillingAdmin
					? [
							{
								value: "billing-admin" as const,
								label: tBilling("admin.nav_label"),
								href: `/dashboard/${workspaceId}/user-settings/billing-admin`,
								icon: <SlidersHorizontal className="h-4 w-4" />,
							},
						]
					: []),
				{
					value: "announcements" as const,
					label: "Announcements",
					href: `/dashboard/${workspaceId}/user-settings/announcements`,
					icon: <Megaphone className="h-4 w-4" />,
				},
				{
					value: "prompts" as const,
					label: "My Prompts",
					href: `/dashboard/${workspaceId}/user-settings/prompts`,
					icon: <WandSparkles className="h-4 w-4" />,
				},
				{
					value: "community-prompts" as const,
					label: "Community Prompts",
					href: `/dashboard/${workspaceId}/user-settings/community-prompts`,
					icon: <Library className="h-4 w-4" />,
				},
				{
					value: "agent-permissions" as const,
					label: "Agent Permissions",
					href: `/dashboard/${workspaceId}/user-settings/agent-permissions`,
					icon: <ShieldCheck className="h-4 w-4" />,
				},
				...(isDesktop
					? [
							{
								value: "desktop" as const,
								label: "App Preferences",
								href: `/dashboard/${workspaceId}/user-settings/desktop`,
								icon: <Monitor className="h-4 w-4" />,
							},
							{
								value: "hotkeys" as const,
								label: "Hotkeys",
								href: `/dashboard/${workspaceId}/user-settings/hotkeys`,
								icon: <Keyboard className="h-4 w-4" />,
							},
						]
					: []),
			].filter((item) => showsUserSettingsTab(item.value, who)),
		[t, tBilling, isBillingAdmin, isDesktop, workspaceId, who]
	);

	// Hidden sections keep their pages; an address typed in goes to Profile.
	const leaving = segment !== null && leavesTab(segment, who, showsUserSettingsTab);
	const waiting = segment !== null && !showsUserSettingsTab(segment, who) && !leaving;
	useEffect(() => {
		if (leaving) router.replace(`/dashboard/${workspaceId}/user-settings/${DEFAULT_TAB}`);
	}, [leaving, router, workspaceId]);

	const activeTab: UserSettingsTab =
		segment && navItems.some((item) => item.value === segment)
			? (segment as UserSettingsTab)
			: DEFAULT_TAB;
	const selectedLabel = navItems.find((item) => item.value === activeTab)?.label ?? t("title");

	return (
		<RoutedSectionShell
			title={t("title")}
			items={navItems}
			activeValue={activeTab}
			selectedLabel={selectedLabel}
			contentClassName="md:max-w-3xl"
		>
			{leaving || waiting ? null : children}
		</RoutedSectionShell>
	);
}
