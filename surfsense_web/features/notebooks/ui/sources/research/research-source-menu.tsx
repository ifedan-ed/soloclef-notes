"use client";

import { ChevronDown, Info } from "lucide-react";
import { useTranslations } from "next-intl";
import { Fragment } from "react";
import { Button } from "@/components/ui/button";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuRadioGroup,
	DropdownMenuRadioItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import {
	canTick,
	MAX_RESEARCH_SOURCES,
	pickOnly,
	type SourcePicks,
	searchedSources,
	setSeveral,
	toggleSource,
} from "../../../lib/research-source-picks";
import {
	ACADEMIC_SOURCE_IDS,
	isResearchBoxSource,
	MAY_RETURN_NOTHING,
	type PageReader,
	READ_PAGE_ID,
	type ResearchBoxSource,
	type ResearchSourceId,
	type UnavailableSources,
	WEB_GROUP_MENU_IDS,
} from "../../../model/research-sources";
import { ResultSourceIcon } from "./result-source-icon";
import { SourceCheckboxItem } from "./source-checkbox-item";
import { useResearchSourceName } from "./use-research-source-name";

const GROUPS = [
	{ key: "group_web", ids: WEB_GROUP_MENU_IDS },
	{ key: "group_academic", ids: ACADEMIC_SOURCE_IDS },
] as const;

/** A caveat shown beside a source that may come back empty, explained on hover. */
function SourceCaveat({ source }: { source: ResearchSourceId }) {
	const t = useTranslations("notebook_research");
	return (
		<Tooltip>
			<TooltipTrigger asChild>
				<span className="ml-auto inline-flex items-center gap-1 pl-2 text-[11px] text-muted-foreground">
					{t("may_return_nothing")}
					<Info className="size-3" aria-hidden />
					{/* Touch screens get no tooltip; screen readers get the reason here. */}
					<span className="sr-only">{t(`caveat_${source}`)}</span>
				</span>
			</TooltipTrigger>
			<TooltipContent side="right" className="max-w-64 text-xs">
				{t(`caveat_${source}`)}
			</TooltipContent>
		</Tooltip>
	);
}

/** A source's icon and name, with why it is unavailable or what to expect of it. */
function SourceRow({
	id,
	reason,
	pageReader,
}: {
	id: ResearchBoxSource;
	reason?: string;
	pageReader: PageReader;
}) {
	const t = useTranslations("notebook_research");
	const sourceName = useResearchSourceName();
	return (
		<>
			{/* The name follows; the brand mark would only repeat it to a screen reader. */}
			<span aria-hidden className="contents">
				<ResultSourceIcon source={id} className="size-4" />
			</span>
			<span className="truncate">{sourceName(id)}</span>
			{reason ? (
				<span className="ml-auto pl-2 text-[11px] text-muted-foreground">
					{t(`unavailable_${reason}`)}
				</span>
			) : id === READ_PAGE_ID ? (
				pageReader === "browserbase" ? (
					<span className="ml-auto pl-2 text-[11px] text-muted-foreground">
						{t("page_via_browserbase")}
					</span>
				) : null
			) : MAY_RETURN_NOTHING.has(id as ResearchSourceId) ? (
				<SourceCaveat source={id as ResearchSourceId} />
			) : null}
		</>
	);
}

/** The chip's face: one source's icon and name, or the picked sources' icons and a count. */
function TriggerFace({ picks }: { picks: SourcePicks }) {
	const t = useTranslations("notebook_research");
	const sourceName = useResearchSourceName();
	const searched = searchedSources(picks);
	if (searched.length <= 1) {
		return (
			<>
				<ResultSourceIcon source={picks.source} className="size-3.5 shrink-0" />
				<span className="truncate">{sourceName(picks.source)}</span>
			</>
		);
	}
	return (
		<>
			<span className="flex shrink-0 -space-x-1" aria-hidden>
				{searched.map((id) => (
					<span key={id} className="grid size-4 place-content-center rounded-full bg-background">
						<ResultSourceIcon source={id} className="size-3.5" />
					</span>
				))}
			</span>
			<span className="truncate">{t("sources_picked", { count: searched.length })}</span>
		</>
	);
}

/**
 * Where to search: the open web, a platform, or a scholarly database; or read
 * one page. One click picks one source; "Search several at once" turns the
 * list into checkboxes for up to ``MAX_RESEARCH_SOURCES`` sources.
 */
export function ResearchSourceMenu({
	picks,
	onChange,
	unavailable = {},
	pageReader = "crawler",
}: {
	picks: SourcePicks;
	onChange: (picks: SourcePicks) => void;
	/** Sources shown greyed out with their reason, e.g. no Exa key in this workspace. */
	unavailable?: UnavailableSources;
	/** What "Read a web page" uses here, named beside it when it is Browserbase. */
	pageReader?: PageReader;
}) {
	const t = useTranslations("notebook_research");
	const sourceName = useResearchSourceName();
	const anyUnavailable = Object.keys(unavailable).length > 0;
	const searched = searchedSources(picks);
	const label = t("source_label", {
		source: searched.length > 1 ? searched.map(sourceName).join(", ") : sourceName(picks.source),
	});
	const reasonFor = (id: ResearchBoxSource) => (id === READ_PAGE_ID ? undefined : unavailable[id]);

	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<Button
					type="button"
					variant="outline"
					size="sm"
					className="h-7 min-w-0 shrink gap-1.5 rounded-full px-2.5 text-xs"
					aria-label={label}
				>
					<TriggerFace picks={picks} />
					<ChevronDown className="size-3 shrink-0 opacity-60" />
				</Button>
			</DropdownMenuTrigger>
			<DropdownMenuContent
				align="start"
				className="max-h-80 w-72 max-w-[calc(100vw-2rem)] overflow-y-auto"
			>
				<SourceCheckboxItem
					checked={picks.several}
					onCheckedChange={(on) => onChange(setSeveral(picks, on))}
					className="font-medium"
				>
					{t("search_several")}
				</SourceCheckboxItem>
				{picks.several ? (
					<p className="px-2 pb-1.5 text-[11px] leading-snug text-muted-foreground">
						{t("search_several_hint", { max: MAX_RESEARCH_SOURCES })}
					</p>
				) : null}
				<DropdownMenuSeparator />
				{picks.several ? (
					GROUPS.map((group, index) => (
						<Fragment key={group.key}>
							{index > 0 ? <DropdownMenuSeparator /> : null}
							<DropdownMenuLabel className="text-xs font-medium text-muted-foreground">
								{t(group.key)}
							</DropdownMenuLabel>
							{group.ids.map((id: ResearchBoxSource) => {
								const reason = reasonFor(id);
								if (id === READ_PAGE_ID) {
									// Reads one link, so it is picked on its own.
									return (
										<DropdownMenuItem
											key={id}
											className="pl-8"
											onSelect={() => onChange(pickOnly(picks, id))}
										>
											<SourceRow id={id} pageReader={pageReader} />
										</DropdownMenuItem>
									);
								}
								return (
									<SourceCheckboxItem
										key={id}
										checked={picks.sources.includes(id)}
										disabled={Boolean(reason) || !canTick(picks, id)}
										onCheckedChange={() => onChange(toggleSource(picks, id))}
									>
										<SourceRow id={id} reason={reason} pageReader={pageReader} />
									</SourceCheckboxItem>
								);
							})}
						</Fragment>
					))
				) : (
					<DropdownMenuRadioGroup
						value={picks.source}
						onValueChange={(next) => {
							if (!isResearchBoxSource(next)) return;
							if (reasonFor(next)) return;
							onChange(pickOnly(picks, next));
						}}
					>
						{GROUPS.map((group, index) => (
							<Fragment key={group.key}>
								{index > 0 ? <DropdownMenuSeparator /> : null}
								<DropdownMenuLabel className="text-xs font-medium text-muted-foreground">
									{t(group.key)}
								</DropdownMenuLabel>
								{group.ids.map((id: ResearchBoxSource) => {
									const reason = reasonFor(id);
									return (
										<DropdownMenuRadioItem key={id} value={id} disabled={Boolean(reason)}>
											<SourceRow id={id} reason={reason} pageReader={pageReader} />
										</DropdownMenuRadioItem>
									);
								})}
							</Fragment>
						))}
					</DropdownMenuRadioGroup>
				)}
				{anyUnavailable ? (
					<p className="px-2 pt-1 pb-1.5 text-[11px] leading-snug text-muted-foreground">
						{t("unavailable_hint")}
					</p>
				) : null}
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
