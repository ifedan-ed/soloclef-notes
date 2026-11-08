"use client";

import { useTranslations } from "next-intl";
import { useMemo } from "react";
import { JsonView } from "@/components/json-view";
import { Spinner } from "@/components/ui/spinner";
import { useScraperRun } from "@/hooks/use-scraper-runs";
import { cn } from "@/lib/utils";
import { OutputViewer } from "./output-viewer";

const MAX_OUTPUT_LINES = 200;

/** One-line label for a persisted ``run.progress`` event object. */
function progressLine(event: Record<string, unknown>, stepLabel: string): string {
	const message = typeof event.message === "string" ? event.message : undefined;
	const phase = typeof event.phase === "string" ? event.phase : undefined;
	const current = typeof event.current === "number" ? event.current : undefined;
	const total = typeof event.total === "number" ? event.total : undefined;
	const base = message || (phase ? phase.replace(/_/g, " ") : stepLabel);
	if (current !== undefined) {
		return `${base} (${total !== undefined ? `${current}/${total}` : current})`;
	}
	return base;
}

/** Parse the stored JSONL output into objects, capped for rendering. */
function parseJsonl(text: string | null): { items: unknown[]; total: number } {
	if (!text) return { items: [], total: 0 };
	const lines = text.split("\n").filter((line) => line.trim().length > 0);
	const items: unknown[] = [];
	for (const line of lines.slice(0, MAX_OUTPUT_LINES)) {
		try {
			items.push(JSON.parse(line));
		} catch {
			items.push(line);
		}
	}
	return { items, total: lines.length };
}

export function RunDetail({
	workspaceId,
	runId,
	showTopBorder = true,
}: {
	workspaceId: number;
	runId: string;
	showTopBorder?: boolean;
}) {
	const t = useTranslations("api_runs");
	const { data: run, isLoading, error } = useScraperRun(workspaceId, runId);

	const parsed = useMemo(() => parseJsonl(run?.output_text ?? null), [run?.output_text]);

	if (isLoading) {
		return (
			<div className="flex h-32 items-center justify-center">
				<Spinner size="md" />
			</div>
		);
	}

	if (error) {
		return (
			<p className="p-4 text-sm text-destructive">
				{error.message
					? t("detail_load_failed_detail", { message: error.message })
					: t("detail_load_failed")}
			</p>
		);
	}

	if (!run) return null;

	return (
		<div className={cn("space-y-4 p-4", showTopBorder && "border-t border-border/60")}>
			{run.error && (
				<div className="rounded-md border border-destructive/40 bg-destructive/5 p-3 text-sm text-destructive">
					{run.error}
				</div>
			)}

			{run.progress && run.progress.length > 0 && (
				<div>
					<h4 className="mb-1.5 text-xs font-medium text-muted-foreground">{t("progress")}</h4>
					<div className="space-y-1 rounded-md border border-border/60 bg-muted/40 p-3 font-mono text-xs text-muted-foreground">
						{run.progress.map((event, i) => (
							<div key={i} className="truncate">
								{progressLine(event, t("progress_step"))}
							</div>
						))}
					</div>
				</div>
			)}

			<div>
				<h4 className="mb-1.5 text-xs font-medium text-muted-foreground">{t("input")}</h4>
				<div className="rounded-md border border-border/60 bg-muted/40 p-3">
					<JsonView src={run.input ?? {}} collapsed={2} />
				</div>
			</div>

			<div>
				<h4 className="mb-1.5 text-xs font-medium text-muted-foreground">
					{parsed.total > 0 ? t("output_count", { count: parsed.total }) : t("output")}
				</h4>
				{run.output_text ? (
					<>
						<OutputViewer
							data={{ items: parsed.items }}
							filenameBase={`${run.capability}-${run.id}`}
						/>
						{parsed.total > MAX_OUTPUT_LINES && (
							<p className="mt-2 text-xs text-muted-foreground">
								{t("stored_truncated", { shown: MAX_OUTPUT_LINES, total: parsed.total })}
							</p>
						)}
					</>
				) : (
					<p className="text-sm text-muted-foreground">{t("no_output")}</p>
				)}
			</div>
		</div>
	);
}
