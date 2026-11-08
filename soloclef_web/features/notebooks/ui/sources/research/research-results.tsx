"use client";

import { TriangleAlert, X } from "lucide-react";
import { useTranslations } from "next-intl";
import { useId, useState } from "react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Spinner } from "@/components/ui/spinner";
import { useDelayedFlag } from "../../../hooks/use-delayed-flag";
import {
	canImport,
	importItemsFor,
	initialSelection,
	resultsSelectState,
	selectAllResults,
	toggleResult,
} from "../../../lib/research-selection";
import type { ProviderProgress } from "../../../lib/research-state";
import {
	MAX_RESEARCH_IMPORT,
	type ResearchImportItem,
	type ResearchResult,
} from "../../../model/research";
import { MAY_RETURN_NOTHING, type ResearchSourceId } from "../../../model/research-sources";
import { FailedSourcesNote } from "./failed-sources-note";
import { ResearchResultRow } from "./research-result-row";

/**
 * What a search found, to pick from and add to the notebook. After a search
 * of several sources each row names the source that found it, and sources
 * that did not answer are listed with why.
 */
export function ResearchResults({
	results,
	source,
	providers = [],
	several = false,
	importing,
	importFailed,
	onImport,
	onDismiss,
}: {
	results: ResearchResult[];
	source: ResearchSourceId;
	providers?: ProviderProgress[];
	several?: boolean;
	importing: boolean;
	importFailed: boolean;
	onImport: (items: ResearchImportItem[]) => void;
	onDismiss: () => void;
}) {
	const t = useTranslations("notebook_research");
	const selectAllId = useId();
	const [selected, setSelected] = useState(() => initialSelection(results));
	const showSpinner = useDelayedFlag(importing);
	const state = resultsSelectState(results, selected);
	const items = importItemsFor(results, selected);
	const overLimit = items.length > MAX_RESEARCH_IMPORT;

	return (
		<section className="rounded-xl border bg-background" aria-label={t("results_label")}>
			<div className="flex items-center gap-2 py-1.5 pr-1.5 pl-3">
				<p className="min-w-0 flex-1 truncate text-xs font-medium">
					{t("results_found", { count: results.length })}
				</p>
				<Button
					type="button"
					variant="ghost"
					size="icon"
					className="size-7 shrink-0 rounded-full"
					onClick={onDismiss}
					aria-label={t("dismiss")}
				>
					<X className="size-4" />
				</Button>
			</div>
			<FailedSourcesNote providers={providers} />
			{results.length === 0 ? (
				<p className="px-3 pb-3 text-xs text-muted-foreground">
					{!several && MAY_RETURN_NOTHING.has(source) ? t(`no_results_${source}`) : t("no_results")}
				</p>
			) : (
				<>
					<div className="flex items-center gap-2 border-t px-4 py-1.5 text-xs text-muted-foreground">
						<label htmlFor={selectAllId} className="flex-1 cursor-pointer">
							{t("select_all")}
						</label>
						<Checkbox
							id={selectAllId}
							checked={state === "all" ? true : state === "some" ? "indeterminate" : false}
							onCheckedChange={() => setSelected(selectAllResults(results, state))}
						/>
					</div>
					<ul className="max-h-80 overflow-y-auto px-1 pb-1">
						{results.map((result) => (
							<ResearchResultRow
								key={result.id}
								result={result}
								checked={selected.has(result.id)}
								showSource={several}
								onToggle={() => setSelected((current) => toggleResult(current, result.id))}
							/>
						))}
					</ul>
					<div className="space-y-1.5 border-t p-2">
						{overLimit ? (
							<p className="px-1 text-xs text-muted-foreground">
								{t("import_limit", { max: MAX_RESEARCH_IMPORT })}
							</p>
						) : null}
						{importFailed ? (
							<p role="alert" className="flex items-start gap-1.5 px-1 text-xs text-destructive">
								<TriangleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden />
								{t("import_failed")}
							</p>
						) : null}
						<Button
							type="button"
							size="sm"
							className="w-full rounded-full"
							disabled={!canImport(items) || importing}
							onClick={() => onImport(items)}
						>
							{showSpinner ? <Spinner size="xs" /> : null}
							{importFailed
								? t("retry_add", { count: items.length })
								: t("add_n", { count: items.length })}
						</Button>
					</div>
				</>
			)}
		</section>
	);
}
