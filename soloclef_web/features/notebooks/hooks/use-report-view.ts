"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { notebookKeys } from "../api/notebook-query-keys";
import { notebooksApi } from "../api/notebooks-api";
import type { ReportView } from "../model/notebook";

/** Errors are shown on the card or button that failed, not as a global toast. */
const OWN_ERROR_TOAST = { suppressGlobalErrorToast: true } as const;

/**
 * A report's reading-view state: its section suggestions (made on the first
 * open, then cached with the report version on the server) and sources.
 * Every mutation answers with the whole view, which replaces the
 * cached one, so cards change state without a refetch.
 */
export function useReportView(
	workspaceId: number,
	notebookId: number,
	outputId: number,
	enabled: boolean
) {
	const queryClient = useQueryClient();
	const key = notebookKeys.reportView(workspaceId, notebookId, outputId);
	const view = useQuery({
		queryKey: key,
		queryFn: () => notebooksApi.reportView(workspaceId, notebookId, outputId),
		enabled: enabled && workspaceId > 0 && notebookId > 0 && outputId > 0,
		staleTime: 60_000,
		refetchOnWindowFocus: false,
	});
	const store = (next: ReportView) => queryClient.setQueryData(key, next);

	const add = useMutation({
		meta: OWN_ERROR_TOAST,
		mutationFn: (suggestionId: string) =>
			notebooksApi.addReportSuggestion(workspaceId, notebookId, outputId, suggestionId),
		onSuccess: (next) => {
			store(next);
			// The new output shows in the Studio list (and is polled from there).
			void queryClient.invalidateQueries({
				queryKey: notebookKeys.outputs(workspaceId, notebookId),
			});
		},
	});
	const dismiss = useMutation({
		meta: OWN_ERROR_TOAST,
		mutationFn: (suggestionId: string) =>
			notebooksApi.dismissReportSuggestion(workspaceId, notebookId, outputId, suggestionId),
		onMutate: (suggestionId) => {
			const previous = queryClient.getQueryData<ReportView>(key);
			if (previous) {
				store({
					...previous,
					suggestions: previous.suggestions.map((s) =>
						s.id === suggestionId ? { ...s, state: "dismissed" } : s
					),
				});
			}
			return { previous };
		},
		onError: (_error, _id, context) => {
			if (context?.previous) store(context.previous);
		},
		onSuccess: store,
	});
	return { view, add, dismiss };
}
