"use client";

import { useInfiniteQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight, History } from "lucide-react";
import { useTranslations } from "next-intl";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Select,
	SelectContent,
	SelectGroup,
	SelectItem,
	SelectLabel,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Spinner } from "@/components/ui/spinner";
import { scrapersApiService } from "@/lib/apis/scrapers-api.service";
import { formatRelativeDate } from "@/lib/format-date";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { cn } from "@/lib/utils";
import {
	isRunOrigin,
	RESEARCH_RUN_CAPABILITIES,
	RUN_CAPABILITIES,
	RUN_STATUSES,
} from "./lib/capabilities";
import { formatCost, formatDuration } from "./lib/format";
import { RunDetail } from "./run-detail";
import { RunStatusBadge } from "./run-status-badge";

const PAGE_SIZE = 25;
const ALL = "__all__";

// A grid (not a <table>) keeps an expanded row's detail out of a table-cell,
// where overflow/max-height scroll containers get ignored. Tracks are fr/fixed
// (never content-based auto) so separate per-row grids stay column-aligned.
const ROW_GRID =
	"grid grid-cols-[2rem_minmax(8rem,2fr)_minmax(4rem,1fr)_minmax(5rem,1fr)_4rem_5rem_5rem_minmax(6rem,1fr)] items-center gap-2 px-3";

export function RunsTable({ workspaceId }: { workspaceId: number }) {
	const t = useTranslations("userSettings");
	const r = useTranslations("api_runs");
	const [capability, setCapability] = useState<string>(ALL);
	const [status, setStatus] = useState<string>(ALL);
	const [expanded, setExpanded] = useState<string | null>(null);

	const filters = {
		capability: capability === ALL ? undefined : capability,
		status: status === ALL ? undefined : status,
	};

	const query = useInfiniteQuery({
		queryKey: [...cacheKeys.scrapers.runs(workspaceId), filters],
		queryFn: ({ pageParam }) =>
			scrapersApiService.listRuns(workspaceId, {
				limit: PAGE_SIZE,
				offset: pageParam,
				...filters,
			}),
		initialPageParam: 0,
		getNextPageParam: (lastPage, allPages) =>
			lastPage.length === PAGE_SIZE ? allPages.length * PAGE_SIZE : undefined,
		// Poll while any visible run is still in flight so it flips to a terminal
		// state without a manual refresh; idle otherwise.
		refetchInterval: (q) =>
			q.state.data?.pages.flat().some((r) => r.status === "running") ? 5000 : false,
	});

	const runs = query.data?.pages.flat() ?? [];

	return (
		<div className="space-y-4">
			<div className="flex flex-wrap items-center gap-2">
				<Select value={capability} onValueChange={setCapability}>
					<SelectTrigger className="w-52">
						<SelectValue placeholder={r("filter_all_apis")} />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value={ALL}>{r("filter_all_apis")}</SelectItem>
						<SelectGroup>
							<SelectLabel>{r("group_scrapers")}</SelectLabel>
							{RUN_CAPABILITIES.map((name) => (
								<SelectItem key={name} value={name}>
									{name}
								</SelectItem>
							))}
						</SelectGroup>
						<SelectGroup>
							<SelectLabel>{r("group_research")}</SelectLabel>
							{RESEARCH_RUN_CAPABILITIES.map((name) => (
								<SelectItem key={name} value={name}>
									{name}
								</SelectItem>
							))}
						</SelectGroup>
					</SelectContent>
				</Select>
				<Select value={status} onValueChange={setStatus}>
					<SelectTrigger className="w-40">
						<SelectValue placeholder={r("filter_all_statuses")} />
					</SelectTrigger>
					<SelectContent>
						<SelectItem value={ALL}>{r("filter_all_statuses")}</SelectItem>
						{RUN_STATUSES.map((value) => (
							<SelectItem key={value} value={value}>
								{r(`status_${value}`)}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
			</div>

			{query.isLoading ? (
				<div className="flex h-48 items-center justify-center">
					<Spinner size="lg" />
				</div>
			) : query.isError ? (
				<p className="text-sm text-destructive">
					{query.error.message
						? r("load_failed_detail", { message: query.error.message })
						: r("load_failed")}
				</p>
			) : runs.length === 0 ? (
				<div className="rounded-md border border-dashed border-border/60 bg-muted/20 px-4 py-12 text-center">
					<History className="mx-auto h-8 w-8 text-muted-foreground" aria-hidden />
					<p className="mt-2 text-sm font-medium">{t("api_runs_empty")}</p>
					<p className="mt-1 text-xs text-muted-foreground">{t("api_runs_empty_hint")}</p>
				</div>
			) : (
				<div className="overflow-hidden rounded-md border border-border/60">
					<div
						className={cn(
							ROW_GRID,
							"border-b border-border/60 bg-muted/30 py-2 text-xs font-medium text-muted-foreground"
						)}
					>
						<span />
						<span>{r("col_api")}</span>
						<span>{r("col_origin")}</span>
						<span>{r("col_status")}</span>
						<span className="text-right">{r("col_items")}</span>
						<span className="text-right">{r("col_duration")}</span>
						<span className="text-right">{r("col_cost")}</span>
						<span className="text-right">{r("col_when")}</span>
					</div>
					{runs.map((run) => {
						const isOpen = expanded === run.id;
						return (
							<div key={run.id} className="border-b border-border/60 last:border-b-0">
								<button
									type="button"
									onClick={() => setExpanded(isOpen ? null : run.id)}
									className={cn(
										ROW_GRID,
										"w-full py-2.5 text-left text-sm transition-colors hover:bg-muted/50"
									)}
								>
									{isOpen ? (
										<ChevronDown className="h-4 w-4 text-muted-foreground" />
									) : (
										<ChevronRight className="h-4 w-4 text-muted-foreground" />
									)}
									<span className="min-w-0 truncate font-mono text-xs">{run.capability}</span>
									<span className="min-w-0 truncate text-xs text-muted-foreground">
										{isRunOrigin(run.origin) ? r(`origin_${run.origin}`) : run.origin}
									</span>
									<span>
										<RunStatusBadge status={run.status} />
									</span>
									<span className="text-right tabular-nums">{run.item_count}</span>
									<span className="text-right tabular-nums text-muted-foreground">
										{formatDuration(run.duration_ms)}
									</span>
									<span className="text-right tabular-nums text-muted-foreground">
										{formatCost(run.cost_micros, r("cost_free"))}
									</span>
									<span className="text-right text-xs text-muted-foreground">
										{formatRelativeDate(run.created_at)}
									</span>
								</button>
								{isOpen && <RunDetail workspaceId={workspaceId} runId={run.id} />}
							</div>
						);
					})}
				</div>
			)}

			{query.hasNextPage && (
				<div className="flex justify-center">
					<Button
						type="button"
						variant="outline"
						onClick={() => query.fetchNextPage()}
						disabled={query.isFetchingNextPage}
						className={cn(query.isFetchingNextPage && "opacity-70")}
					>
						{query.isFetchingNextPage ? r("loading_more") : r("load_more")}
					</Button>
				</div>
			)}
		</div>
	);
}
