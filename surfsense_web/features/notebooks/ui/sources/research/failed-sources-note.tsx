"use client";

import { TriangleAlert } from "lucide-react";
import { useTranslations } from "next-intl";
import type { ProviderProgress } from "../../../lib/research-state";
import { isResearchBoxSource } from "../../../model/research-sources";
import { useSourceFailureReason } from "./use-source-failure-reason";

/** Which sources of a several-source search did not answer, and why, above what the others found. */
export function FailedSourcesNote({ providers }: { providers: ProviderProgress[] }) {
	const t = useTranslations("notebook_research");
	const reasonFor = useSourceFailureReason();
	const failed = providers.filter((p) => p.status === "error");
	if (failed.length === 0) return null;
	return (
		<div className="space-y-0.5 border-t px-3 py-1.5 text-[11px] text-muted-foreground">
			<p className="flex items-center gap-1.5 font-medium text-foreground">
				<TriangleAlert className="size-3 shrink-0 text-destructive" aria-hidden />
				{t("some_sources_failed")}
			</p>
			<ul className="space-y-0.5 pl-4.5">
				{failed.map((provider) => (
					<li key={provider.source} className="break-words">
						{isResearchBoxSource(provider.source)
							? reasonFor(provider.source, provider.code, provider.message)
							: provider.message}
					</li>
				))}
			</ul>
		</div>
	);
}
