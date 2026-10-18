"use client";

import { useQuery } from "@tanstack/react-query";
import { useMemo } from "react";
import { notebookKeys } from "@/features/notebooks/api/notebook-query-keys";
import { researchApi } from "@/features/notebooks/api/research-api";
import type { KeyedToolId } from "./composer-tools";
import { connectedKeyedTools } from "./connected-keyed-tools";

/**
 * The keyed tools (Exa, Tavily, Browserbase) the composer can offer in this
 * notebook's chat. Shares the research box's status request; outside a
 * notebook nothing is asked and none are offered.
 */
export function useConnectedKeyedTools(
	workspaceId: number,
	notebookId: number | null | undefined
): ReadonlySet<KeyedToolId> {
	const id = notebookId ?? 0;
	const { data } = useQuery({
		queryKey: notebookKeys.researchSources(workspaceId, id),
		queryFn: () => researchApi.sourcesStatus(workspaceId, id),
		enabled: workspaceId > 0 && id > 0,
		// Up to a minute old: a pick whose key was removed meanwhile is told
		// "not available" by the backend (SourcesOnly.unavailable_picks).
		staleTime: 60_000,
		// A background check: its failure is not worth a toast.
		meta: { suppressGlobalErrorToast: true },
	});
	return useMemo(() => connectedKeyedTools(data), [data]);
}
