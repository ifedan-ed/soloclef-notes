"use client";

import { useLocale, useTranslations } from "next-intl";
import { useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import {
	Select,
	SelectContent,
	SelectItem,
	SelectTrigger,
	SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import {
	Table,
	TableBody,
	TableCell,
	TableHead,
	TableHeader,
	TableRow,
} from "@/components/ui/table";
import { useDelayedFlag } from "@/features/notebooks/hooks/use-delayed-flag";
import type { Price, UsagePage } from "../api/billing-schemas";
import { useBillingUsage } from "../hooks/use-billing-queries";
import { formatCredits, formatDateTime } from "../lib/format";
import { SectionHeading } from "./section-heading";

const PAGE_SIZE = 20;
const ALL = "__all__";

/** Recent metered actions with a filter, simple paging and a per-action total. */
export function UsageHistory({ prices }: { prices: Price[] }) {
	const t = useTranslations("billing");
	const [action, setAction] = useState(ALL);
	const [page, setPage] = useState(0);
	const query = useMemo(
		() => ({
			limit: PAGE_SIZE,
			offset: page * PAGE_SIZE,
			action: action === ALL ? undefined : action,
		}),
		[action, page]
	);
	const usage = useBillingUsage(query);
	const showSkeleton = useDelayedFlag(usage.isLoading);
	const labels = useMemo(() => new Map(prices.map((p) => [p.action, p.label])), [prices]);
	const pages = usage.data ? Math.max(1, Math.ceil(usage.data.total / PAGE_SIZE)) : 1;

	return (
		<section className="space-y-3" aria-labelledby="billing-usage-title">
			<div id="billing-usage-title">
				<SectionHeading
					title={t("usage.title")}
					description={t("usage.description")}
					action={
						<Select
							value={action}
							onValueChange={(value) => {
								setAction(value);
								setPage(0);
							}}
						>
							<SelectTrigger className="w-full sm:w-56" aria-label={t("usage.filter")}>
								<SelectValue />
							</SelectTrigger>
							<SelectContent>
								<SelectItem value={ALL}>{t("usage.all_actions")}</SelectItem>
								{prices.map((price) => (
									<SelectItem key={price.action} value={price.action}>
										{price.label}
									</SelectItem>
								))}
							</SelectContent>
						</Select>
					}
				/>
			</div>

			{usage.data ? (
				<UsageTable data={usage.data} labels={labels} />
			) : usage.isError ? (
				<p className="text-sm text-destructive">{t("errors.load_failed")}</p>
			) : showSkeleton ? (
				<div className="space-y-2" aria-busy>
					<Skeleton className="h-8 w-full" />
					<Skeleton className="h-8 w-full" />
				</div>
			) : null}

			{usage.data && usage.data.total > PAGE_SIZE ? (
				<div className="flex items-center justify-between gap-2">
					<Button
						variant="outline"
						size="sm"
						disabled={page === 0}
						onClick={() => setPage((p) => Math.max(0, p - 1))}
					>
						{t("usage.previous")}
					</Button>
					<span className="text-sm text-muted-foreground">
						{t("usage.page_of", { page: page + 1, pages })}
					</span>
					<Button
						variant="outline"
						size="sm"
						disabled={page + 1 >= pages}
						onClick={() => setPage((p) => p + 1)}
					>
						{t("usage.next")}
					</Button>
				</div>
			) : null}

			{usage.data && usage.data.by_action.length > 0 ? (
				<UsageSummary data={usage.data} labels={labels} />
			) : null}
		</section>
	);
}

export function UsageTable({ data, labels }: { data: UsagePage; labels: Map<string, string> }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	if (data.items.length === 0) {
		return <p className="text-sm text-muted-foreground">{t("usage.empty")}</p>;
	}
	return (
		<Table>
			<TableHeader>
				<TableRow>
					<TableHead>{t("usage.date")}</TableHead>
					<TableHead>{t("usage.action")}</TableHead>
					<TableHead className="text-right">{t("usage.units")}</TableHead>
					<TableHead className="text-right">{t("usage.credits")}</TableHead>
					<TableHead>{t("usage.status")}</TableHead>
					<TableHead>{t("usage.notebook")}</TableHead>
				</TableRow>
			</TableHeader>
			<TableBody>
				{data.items.map((row) => (
					<TableRow key={row.id}>
						<TableCell className="whitespace-nowrap">
							{formatDateTime(row.created_at, locale)}
						</TableCell>
						<TableCell>{labels.get(row.action) ?? row.action}</TableCell>
						<TableCell className="text-right tabular-nums">{row.units}</TableCell>
						<TableCell className="text-right tabular-nums">
							{formatCredits(row.credits, locale)}
						</TableCell>
						<TableCell className="whitespace-nowrap">
							{t.has(`usage_status.${row.status}`) ? t(`usage_status.${row.status}`) : row.status}
							{row.mode === "shadow" ? (
								<span className="ml-1 text-xs text-muted-foreground">{t("usage.not_charged")}</span>
							) : null}
						</TableCell>
						<TableCell className="tabular-nums">
							{row.notebook_id != null ? `#${row.notebook_id}` : "—"}
						</TableCell>
					</TableRow>
				))}
			</TableBody>
		</Table>
	);
}

function UsageSummary({ data, labels }: { data: UsagePage; labels: Map<string, string> }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	return (
		<div className="rounded-lg border p-3">
			<h3 className="text-sm font-medium">{t("usage.by_action")}</h3>
			<ul className="mt-2 space-y-1 text-sm">
				{data.by_action.map((row) => (
					<li key={row.action} className="flex justify-between gap-3">
						<span className="min-w-0 truncate">{labels.get(row.action) ?? row.action}</span>
						<span className="shrink-0 tabular-nums text-muted-foreground">
							{t("usage.summary_line", {
								count: row.count,
								credits: formatCredits(row.credits, locale),
							})}
						</span>
					</li>
				))}
			</ul>
		</div>
	);
}
