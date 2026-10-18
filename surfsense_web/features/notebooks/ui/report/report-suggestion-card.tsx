"use client";

import { Plus, RotateCcw, Sparkles, TriangleAlert, X } from "lucide-react";
import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { knownPhase } from "../../lib/output-phase";
import type { SuggestionPhase } from "../../lib/report-suggestions";
import { studioTile } from "../../lib/studio-catalog";
import type { ReportSuggestion, StudioOutput } from "../../model/notebook";

/**
 * One suggestion under a report section: the offer ("Recommended · Quiz"),
 * then, once added, the output being made in place (ReportEmbedCard shows it
 * once it is ready), or the failure with a retry.
 */
export function ReportSuggestionCard({
	suggestion,
	phase,
	output,
	error,
	onAdd,
	onDismiss,
}: {
	suggestion: ReportSuggestion;
	phase: Exclude<SuggestionPhase, "ready">;
	output: StudioOutput | undefined;
	error: boolean;
	onAdd: () => void;
	onDismiss: () => void;
}) {
	const t = useTranslations("report_view");
	const tn = useTranslations("notebooks");
	const kindLabel = tn(`kinds.${suggestion.kind}.label`);
	const Icon = studioTile(suggestion.kind).icon;

	if (phase === "starting" || phase === "running") {
		const step =
			phase === "running" && output?.status === "running" ? knownPhase(output.phase) : null;
		const progress = output?.progress ? `${output.progress}%` : null;
		return (
			<output
				data-report-suggestion={suggestion.id}
				className="not-prose my-6 flex min-w-0 items-center gap-3 rounded-2xl border bg-muted/40 px-4 py-3"
			>
				<Spinner size="sm" className="shrink-0" />
				<span className="block min-w-0 text-sm">
					<span className="font-medium">{t("generating", { kind: kindLabel })}</span>
					<span className="text-muted-foreground"> · {suggestion.title}</span>
					{step || progress ? (
						<span className="block text-xs text-muted-foreground">
							{[step ? tn(`phases.${step}`) : null, progress].filter(Boolean).join(" · ")}
						</span>
					) : null}
				</span>
			</output>
		);
	}

	const failed = phase === "failed";
	return (
		<div
			data-report-suggestion={suggestion.id}
			className="not-prose my-6 rounded-2xl border bg-muted/30 p-4"
		>
			<div className="flex min-w-0 flex-wrap items-start gap-3 sm:flex-nowrap">
				<span className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-background text-muted-foreground">
					{failed ? (
						<TriangleAlert className="size-4 text-destructive" aria-hidden />
					) : (
						<Icon className="size-4" aria-hidden />
					)}
				</span>
				<div className="min-w-0 flex-1 basis-[calc(100%-3rem)] sm:basis-auto">
					<p className="flex items-center gap-1.5 text-xs text-muted-foreground">
						{failed ? (
							t("failed", { kind: kindLabel })
						) : (
							<>
								<Sparkles className="size-3" aria-hidden />
								{t("recommended")} · {kindLabel}
							</>
						)}
					</p>
					<p className="mt-0.5 break-words font-medium">{suggestion.title}</p>
					{suggestion.focus ? (
						<p className="mt-1 break-words text-sm text-muted-foreground">{suggestion.focus}</p>
					) : null}
					{error ? (
						<p role="alert" className="mt-2 text-sm text-destructive">
							{t("start_failed")}
						</p>
					) : null}
				</div>
				<div className="ml-auto flex shrink-0 items-center gap-1">
					<Button
						size="sm"
						variant={failed ? "outline" : "default"}
						className="rounded-full"
						onClick={onAdd}
						aria-label={
							failed
								? t("try_again_label", { title: suggestion.title })
								: t("add_label", { kind: kindLabel, title: suggestion.title })
						}
					>
						{failed ? <RotateCcw className="size-4" /> : <Plus className="size-4" />}
						{failed ? t("try_again") : t("add")}
					</Button>
					<Button
						size="icon"
						variant="ghost"
						className="size-8 rounded-full text-muted-foreground"
						onClick={onDismiss}
						aria-label={t("dismiss", { title: suggestion.title })}
					>
						<X className="size-4" />
					</Button>
				</div>
			</div>
		</div>
	);
}
