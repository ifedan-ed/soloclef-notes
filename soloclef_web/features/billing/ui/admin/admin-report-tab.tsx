"use client";

import { useLocale, useTranslations } from "next-intl";
import { useMemo, useState } from "react";
import { SegmentedControl } from "@/components/ui/segmented-control";
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
import type { AdminOverview } from "../../api/billing-admin-schemas";
import { useAdminReport } from "../../hooks/use-billing-admin";
import { formatCredits } from "../../lib/format";

const DAY_OPTIONS = ["7", "30", "90"] as const;

/** Real usage by action (shadow and live), by person, and what was or would be refused. */
export function AdminReportTab({ overview }: { overview: AdminOverview }) {
	const t = useTranslations("billing");
	const locale = useLocale();
	const [days, setDays] = useState<string>("30");
	const report = useAdminReport(Number(days));
	const showSkeleton = useDelayedFlag(report.isLoading);
	const labels = useMemo(
		() => new Map(overview.prices.map((p) => [p.action, p.label])),
		[overview.prices]
	);
	const usd = (value: number) =>
		new Intl.NumberFormat(locale, {
			style: "currency",
			currency: "USD",
			maximumFractionDigits: 4,
		}).format(value);

	return (
		<div className="space-y-6">
			<SegmentedControl
				ariaLabel={t("admin.report_days")}
				value={days}
				onValueChange={setDays}
				options={DAY_OPTIONS.map((d) => ({
					value: d,
					label: t("admin.days", { count: Number(d) }),
				}))}
			/>
			{report.isError ? (
				<p className="text-sm text-destructive">{t("errors.load_failed")}</p>
			) : !report.data ? (
				showSkeleton ? (
					<Skeleton className="h-48 w-full" />
				) : null
			) : (
				<>
					<section className="space-y-2">
						<h3 className="text-sm font-semibold">{t("admin.report_by_action")}</h3>
						{report.data.by_action.length === 0 ? (
							<p className="text-sm text-muted-foreground">{t("admin.report_empty")}</p>
						) : (
							<Table>
								<TableHeader>
									<TableRow>
										<TableHead>{t("usage.action")}</TableHead>
										<TableHead>{t("admin.mode_title")}</TableHead>
										<TableHead className="text-right">{t("admin.count")}</TableHead>
										<TableHead className="text-right">{t("usage.units")}</TableHead>
										<TableHead className="text-right">{t("usage.credits")}</TableHead>
										<TableHead className="text-right">{t("admin.would_refuse")}</TableHead>
										<TableHead className="text-right">{t("admin.provider_cost")}</TableHead>
									</TableRow>
								</TableHeader>
								<TableBody>
									{report.data.by_action.map((row) => (
										<TableRow key={`${row.action}-${row.mode}`}>
											<TableCell>{labels.get(row.action) ?? row.action}</TableCell>
											<TableCell>
												{t.has(`admin.modes.${row.mode}`) ? t(`admin.modes.${row.mode}`) : row.mode}
											</TableCell>
											<TableCell className="text-right tabular-nums">{row.count}</TableCell>
											<TableCell className="text-right tabular-nums">{row.units}</TableCell>
											<TableCell className="text-right tabular-nums">
												{formatCredits(row.credits, locale)}
											</TableCell>
											<TableCell className="text-right tabular-nums">{row.would_refuse}</TableCell>
											<TableCell className="text-right tabular-nums">
												{usd(row.provider_cost_usd)}
											</TableCell>
										</TableRow>
									))}
								</TableBody>
							</Table>
						)}
					</section>

					<section className="space-y-2">
						<h3 className="text-sm font-semibold">{t("admin.report_by_user")}</h3>
						{report.data.by_user.length === 0 ? (
							<p className="text-sm text-muted-foreground">{t("admin.report_empty")}</p>
						) : (
							<ul className="divide-y rounded-lg border text-sm">
								{report.data.by_user.map((row) => (
									<li key={row.email} className="flex justify-between gap-3 p-2">
										<span className="min-w-0 break-all">{row.email}</span>
										<span className="shrink-0 tabular-nums text-muted-foreground">
											{t("usage.summary_line", {
												count: row.count,
												credits: formatCredits(row.credits, locale),
											})}
										</span>
									</li>
								))}
							</ul>
						)}
					</section>

					<section className="space-y-2">
						<h3 className="text-sm font-semibold">{t("admin.report_denials")}</h3>
						{report.data.denials.length === 0 ? (
							<p className="text-sm text-muted-foreground">{t("admin.report_no_denials")}</p>
						) : (
							<ul className="divide-y rounded-lg border text-sm">
								{report.data.denials.map((row) => (
									<li key={row.code ?? "none"} className="flex justify-between gap-3 p-2">
										<span className="min-w-0 break-all font-mono text-xs">{row.code}</span>
										<span className="tabular-nums">{row.count}</span>
									</li>
								))}
							</ul>
						)}
					</section>
				</>
			)}
		</div>
	);
}
