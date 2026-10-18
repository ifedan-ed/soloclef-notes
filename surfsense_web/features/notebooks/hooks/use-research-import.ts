"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { notebookKeys } from "../api/notebook-query-keys";
import { researchApi } from "../api/research-api";
import type { ResearchImportItem } from "../model/research";

/**
 * Adds chosen web results as notebook sources. They come back pending; the
 * sources query keeps polling until indexing flips them to ready.
 */
export function useResearchImport(workspaceId: number, notebookId: number) {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: (items: ResearchImportItem[]) =>
			researchApi.importResults(workspaceId, notebookId, items),
		onSuccess: (sources) => {
			queryClient.setQueryData(notebookKeys.sources(workspaceId, notebookId), sources);
			void queryClient.invalidateQueries({
				queryKey: notebookKeys.detail(workspaceId, notebookId),
			});
			void queryClient.invalidateQueries({ queryKey: notebookKeys.list(workspaceId) });
		},
	});
}
