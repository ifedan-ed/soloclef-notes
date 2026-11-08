"use client";

import { Check, X } from "lucide-react";
import { useTranslations } from "next-intl";
import { cn } from "@/lib/utils";
import type { ProviderProgress } from "../../../lib/research-state";
import { isResearchBoxSource } from "../../../model/research-sources";
import { ResultSourceIcon } from "./result-source-icon";
import { useResearchSourceName } from "./use-research-source-name";

/** How each source of a several-source search is getting on: looking, answered, or failed. */
export function ProviderStatusList({ providers }: { providers: ProviderProgress[] }) {
	const t = useTranslations("notebook_research");
	const sourceName = useResearchSourceName();
	if (providers.length === 0) return null;
	return (
		<ul className="flex flex-wrap gap-1.5">
			{providers.map((provider) => {
				const name = isResearchBoxSource(provider.source)
					? sourceName(provider.source)
					: provider.source;
				const label =
					provider.status === "done"
						? t("provider_done", { source: name, count: provider.count ?? 0 })
						: provider.status === "error"
							? t("provider_error", { source: name })
							: t("provider_searching", { source: name });
				return (
					<li
						key={provider.source}
						title={label}
						className={cn(
							"inline-flex min-w-0 items-center gap-1 rounded-full border bg-background px-2 py-0.5 text-[11px]",
							provider.status === "error" && "border-destructive/30 text-destructive"
						)}
					>
						<ResultSourceIcon source={provider.source} className="size-3 shrink-0" />
						<span className="truncate">{name}</span>
						{provider.status === "done" ? (
							<>
								<Check className="size-3 shrink-0 text-muted-foreground" aria-hidden />
								<span className="text-muted-foreground tabular-nums" aria-hidden>
									{provider.count ?? 0}
								</span>
							</>
						) : provider.status === "error" ? (
							<X className="size-3 shrink-0" aria-hidden />
						) : (
							<span
								aria-hidden
								className="size-2.5 shrink-0 animate-spin rounded-full border-[1.5px] border-current/20 border-t-current motion-reduce:animate-none"
							/>
						)}
						<span className="sr-only">{label}</span>
					</li>
				);
			})}
		</ul>
	);
}
