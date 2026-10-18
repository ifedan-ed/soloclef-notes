"use client";

import { useTranslations } from "next-intl";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import type { ProviderProgress } from "../../../lib/research-state";
import type { ResearchPhase } from "../../../model/research";
import { ProviderStatusList } from "./provider-status-list";

/** Where a running search is (and, with several sources, where each is), with a way to stop it. */
export function ResearchProgress({
	phase,
	progress,
	providers = [],
	onCancel,
}: {
	phase: ResearchPhase | null;
	progress: number;
	providers?: ProviderProgress[];
	onCancel: () => void;
}) {
	const t = useTranslations("notebook_research");
	const label = phase ? t(`phase_${phase}`) : t("phase_starting");
	return (
		<div className="space-y-2 rounded-xl bg-muted/50 px-3 py-2.5" aria-live="polite">
			<div className="flex items-center gap-2">
				<p className="min-w-0 flex-1 truncate text-xs font-medium">{label}</p>
				<Button
					type="button"
					variant="ghost"
					size="sm"
					className="h-6 shrink-0 px-2 text-xs"
					onClick={onCancel}
				>
					{t("cancel")}
				</Button>
			</div>
			<Progress
				value={progress}
				className="h-1.5"
				role="progressbar"
				aria-label={label}
				aria-valuemin={0}
				aria-valuemax={100}
				aria-valuenow={Math.round(progress)}
			/>
			<ProviderStatusList providers={providers} />
		</div>
	);
}
