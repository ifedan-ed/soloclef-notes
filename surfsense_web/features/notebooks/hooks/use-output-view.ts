"use client";

import { useQuery } from "@tanstack/react-query";
import { notebookKeys } from "../api/notebook-query-keys";
import { notebooksApi } from "../api/notebooks-api";

/** Errors show in the viewer, not as a global toast. */
const OWN_ERROR_TOAST = { suppressGlobalErrorToast: true } as const;

/**
 * An open Studio output's frame: the prompt it was made with and its sources
 * ("View prompt and N sources").
 */
export function useOutputView(workspaceId: number, notebookId: number, outputId: number) {
	const key = notebookKeys.outputView(workspaceId, notebookId, outputId);
	const view = useQuery({
		queryKey: key,
		queryFn: () => notebooksApi.outputView(workspaceId, notebookId, outputId),
		enabled: workspaceId > 0 && notebookId > 0 && outputId > 0,
		staleTime: 60_000,
		refetchOnWindowFocus: false,
		meta: OWN_ERROR_TOAST,
	});
	return { view };
}
