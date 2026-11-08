"use client";

import { Loader2 } from "lucide-react";
import { useTranslations } from "next-intl";
import { Badge } from "@/components/ui/badge";
import { isRunStatus } from "./lib/capabilities";

/** Scraper runs: ``running`` (async, in-flight), ``success``, ``error``, ``cancelled``. */
export function RunStatusBadge({ status }: { status: string }) {
	const t = useTranslations("api_runs");
	const normalized = status.toLowerCase();
	const label = isRunStatus(normalized) ? t(`status_${normalized}`) : status;
	if (normalized === "running") {
		return (
			<Badge variant="secondary" className="gap-1 bg-blue-500/15 text-blue-600 dark:text-blue-400">
				<Loader2 className="h-3 w-3 animate-spin" />
				{label}
			</Badge>
		);
	}
	if (normalized === "success") {
		return (
			<Badge
				variant="secondary"
				className="bg-emerald-500/15 text-emerald-600 dark:text-emerald-400"
			>
				{label}
			</Badge>
		);
	}
	if (normalized === "error") {
		return <Badge variant="destructive">{label}</Badge>;
	}
	if (normalized === "cancelled") {
		return (
			<Badge variant="secondary" className="bg-amber-500/15 text-amber-600 dark:text-amber-400">
				{label}
			</Badge>
		);
	}
	return <Badge variant="outline">{label}</Badge>;
}
