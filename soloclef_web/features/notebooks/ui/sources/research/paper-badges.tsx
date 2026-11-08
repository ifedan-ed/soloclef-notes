"use client";

import { LockOpen } from "lucide-react";
import { useTranslations } from "next-intl";
import { type Paper, type PaperBadge, paperBadges } from "../../../model/research";

function isKnownBadge(value: string): value is PaperBadge {
	return (paperBadges as readonly string[]).includes(value);
}

/** Where a paper is indexed (PubMed, PMC, arXiv, ...) and whether it is free to read. */
export function PaperBadges({ paper }: { paper: Paper }) {
	const t = useTranslations("notebook_research");
	const badges = paper.badges.filter(isKnownBadge);
	if (badges.length === 0 && !paper.open_access) return null;
	return (
		<span className="mt-0.5 flex flex-wrap items-center gap-1">
			{badges.map((badge) => (
				<span
					key={badge}
					className="rounded border px-1 py-px text-[10px] font-medium leading-none text-muted-foreground"
				>
					{t(`badge_${badge}`)}
				</span>
			))}
			{paper.open_access ? (
				<span
					className="inline-flex items-center gap-0.5 rounded bg-emerald-500/10 px-1 py-px text-[10px] font-medium leading-none text-emerald-700 dark:text-emerald-400"
					title={t("open_access_hint")}
				>
					<LockOpen className="size-2.5" aria-hidden />
					{t("open_access")}
				</span>
			) : null}
		</span>
	);
}
