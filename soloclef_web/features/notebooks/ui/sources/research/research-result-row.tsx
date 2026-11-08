"use client";

import { useTranslations } from "next-intl";
import { Checkbox } from "@/components/ui/checkbox";
import type { ResearchResult } from "../../../model/research";
import { isResearchBoxSource } from "../../../model/research-sources";
import { PaperBadges } from "./paper-badges";
import { ResultSourceIcon } from "./result-source-icon";
import { ResultThumbnail } from "./result-thumbnail";
import { useResearchSourceName } from "./use-research-source-name";

/** Which source found this row, after a search of several. */
function FoundBy({ source }: { source: string }) {
	const t = useTranslations("notebook_research");
	const sourceName = useResearchSourceName();
	const name = isResearchBoxSource(source) ? sourceName(source) : source;
	return (
		<span className="mt-0.5 inline-flex max-w-full items-center gap-1 rounded-full bg-muted px-1.5 py-px text-[10px] text-muted-foreground">
			<ResultSourceIcon source={source} className="size-2.5 shrink-0" />
			<span className="truncate">{t("found_by", { source: name })}</span>
		</span>
	);
}

export function ResearchResultRow({
	result,
	checked,
	onToggle,
	showSource = false,
}: {
	result: ResearchResult;
	checked: boolean;
	onToggle: () => void;
	/** Name the source that found it (after a search of several). */
	showSource?: boolean;
}) {
	const t = useTranslations("notebook_research");
	return (
		<li className="flex min-w-0 items-start gap-2 rounded-lg px-2 py-1.5 hover:bg-accent">
			<ResultThumbnail result={result} />
			<div className="min-w-0 flex-1">
				<a
					href={result.url}
					target="_blank"
					rel="noopener noreferrer"
					title={result.url}
					className="line-clamp-2 break-words text-sm font-medium text-foreground hover:underline"
				>
					{result.title || result.url}
				</a>
				{result.snippet ? (
					<p className="line-clamp-2 break-words text-xs text-muted-foreground">{result.snippet}</p>
				) : null}
				{result.meta ? (
					<p className="truncate text-[11px] text-muted-foreground/80">{result.meta}</p>
				) : null}
				{result.paper ? <PaperBadges paper={result.paper} /> : null}
				{showSource ? <FoundBy source={result.source} /> : null}
			</div>
			<Checkbox
				checked={checked}
				onCheckedChange={onToggle}
				aria-label={t("select_result", { title: result.title || result.url })}
				className="mt-0.5 shrink-0"
			/>
		</li>
	);
}
