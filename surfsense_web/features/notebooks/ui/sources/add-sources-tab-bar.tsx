"use client";

import { useTranslations } from "next-intl";
import { TabsList, TabsTrigger } from "@/components/ui/tabs";

const TABS = [
	{ value: "library", long: "add_from_library", short: "add_from_library_short" },
	{ value: "upload", long: "add_upload", short: "add_upload_short" },
	{ value: "connector", long: "add_from_connector", short: "add_from_connector_short" },
] as const;

/**
 * The three ways to add sources. On a phone the bar spans the dialog in three
 * equal columns with one-word labels, so no tab is cut off; from ``sm`` up it
 * shrinks to fit and spells the labels out.
 */
export function AddSourcesTabBar() {
	const t = useTranslations("notebooks");
	return (
		<TabsList className="grid h-auto w-full grid-cols-3 sm:inline-flex sm:h-10 sm:w-fit">
			{TABS.map((tab) => (
				<TabsTrigger key={tab.value} value={tab.value} className="min-w-0 px-2 sm:px-3">
					<span className="truncate sm:hidden">{t(tab.short)}</span>
					<span className="hidden sm:inline">{t(tab.long)}</span>
				</TabsTrigger>
			))}
		</TabsList>
	);
}
