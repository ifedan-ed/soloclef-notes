"use client";

import { useMutation, useQueryClient } from "@tanstack/react-query";
import { notebookKeys } from "../api/notebook-query-keys";
import { researchApi } from "../api/research-api";

/**
 * "Read a web page": adds a pasted URL as a notebook source. It comes back
 * pending; the sources query keeps polling until the page is read and indexed.
 * Errors are shown inline by the research box, so no global toast.
 */
export function useReadWebPage(workspaceId: number, notebookId: number) {
	const queryClient = useQueryClient();
	return useMutation({
		meta: { suppressGlobalErrorToast: true },
		mutationFn: (url: string) => researchApi.readPage(workspaceId, notebookId, url.trim()),
		onSuccess: (sources) => {
			queryClient.setQueryData(notebookKeys.sources(workspaceId, notebookId), sources);
			void queryClient.invalidateQueries({
				queryKey: notebookKeys.detail(workspaceId, notebookId),
			});
			void queryClient.invalidateQueries({ queryKey: notebookKeys.list(workspaceId) });
		},
	});
}
