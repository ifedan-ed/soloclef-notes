"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

/**
 * For mutations whose callers show their own, worded error toast: without
 * this the global mutation handler shows a second toast for the same failure.
 */
const OWN_ERROR_TOAST = { suppressGlobalErrorToast: true } as const;

import { notebookKeys } from "../api/notebook-query-keys";
import { notebooksApi, type SourceTarget } from "../api/notebooks-api";
import { withToggled } from "../lib/source-selection";
import type { StudioRequestBody } from "../lib/studio-request";
import type { Notebook, NotebookSource } from "../model/notebook";

/*
 * None of these reads polls. The change stream (features/live-changes) reads
 * them again when the server reports a change: `notebooks` (created, renamed,
 * named automatically, sources added or ticked), `documents` (a source's
 * indexing status and stage, counts) and `outputs` (Studio progress and
 * finish). They used to poll while a source indexed, an output generated or
 * an automatic name was due.
 */

export function useNotebooks() {
	return useQuery({
		queryKey: notebookKeys.list(),
		queryFn: () => notebooksApi.list(),
		placeholderData: keepPreviousData,
	});
}

export function useNotebook(notebookId: number) {
	const queryClient = useQueryClient();
	return useQuery({
		queryKey: notebookKeys.detail(notebookId),
		queryFn: () => notebooksApi.get(notebookId),
		enabled: notebookId > 0,
		// Arriving from the list: paint the card's data at once, refresh behind it.
		placeholderData: () =>
			queryClient
				.getQueryData<Notebook[]>(notebookKeys.list())
				?.find((notebook) => notebook.id === notebookId),
	});
}

export function useNotebookSources(notebookId: number) {
	return useQuery({
		queryKey: notebookKeys.sources(notebookId),
		queryFn: () => notebooksApi.sources(notebookId),
		enabled: notebookId > 0,
	});
}

export function useNotebookThreads(notebookId: number) {
	return useQuery({
		queryKey: notebookKeys.threads(notebookId),
		queryFn: () => notebooksApi.threads(notebookId),
		enabled: notebookId > 0,
	});
}

export function useStudioOutputs(notebookId: number) {
	return useQuery({
		queryKey: notebookKeys.outputs(notebookId),
		queryFn: () => notebooksApi.outputs(notebookId),
		enabled: notebookId > 0,
		placeholderData: keepPreviousData,
	});
}

export function useSourceGuide(notebookId: number, sourceId: number | null) {
	return useQuery({
		queryKey: notebookKeys.guide(notebookId, sourceId ?? 0),
		queryFn: () => notebooksApi.sourceGuide(notebookId, sourceId ?? 0),
		enabled: sourceId != null,
		staleTime: 5 * 60_000,
	});
}

export function useStudioSuggestions(
	notebookId: number,
	kind: string,
	{ variant = "", language = "" }: { variant?: string; language?: string },
	enabled: boolean
) {
	return useQuery({
		queryKey: notebookKeys.suggestions(notebookId, kind, variant, language),
		queryFn: () => notebooksApi.suggestions(notebookId, kind, { variant, language }),
		enabled,
		staleTime: 10 * 60_000,
		// Switching format or language keeps the last ideas painted until the
		// new ones arrive, instead of blanking the chips.
		placeholderData: keepPreviousData,
	});
}

export function useReportTemplates(enabled: boolean) {
	return useQuery({
		queryKey: notebookKeys.reportTemplates(),
		queryFn: () => notebooksApi.reportTemplates(),
		enabled,
	});
}

/** Source mutations, applied optimistically so checkboxes answer instantly. */
export function useSourceMutations(notebookId: number) {
	const queryClient = useQueryClient();
	const key = notebookKeys.sources(notebookId);
	const refreshCounts = () => {
		void queryClient.invalidateQueries({ queryKey: notebookKeys.detail(notebookId) });
		void queryClient.invalidateQueries({ queryKey: notebookKeys.list() });
	};

	type ToggleVars = { sourceId: number | "all"; enabled: boolean };
	type Snapshot = { previous?: NotebookSource[] };

	const toggle = useMutation<unknown, Error, ToggleVars, Snapshot>({
		meta: OWN_ERROR_TOAST,
		mutationFn: async ({ sourceId, enabled }) => {
			if (sourceId === "all") {
				await notebooksApi.toggleAllSources(notebookId, enabled);
			} else {
				await notebooksApi.toggleSource(notebookId, sourceId, enabled);
			}
		},
		onMutate: async ({ sourceId, enabled }) => {
			await queryClient.cancelQueries({ queryKey: key });
			const previous = queryClient.getQueryData<NotebookSource[]>(key);
			if (previous) queryClient.setQueryData(key, withToggled(previous, sourceId, enabled));
			return { previous };
		},
		onError: (_error, _vars, context) => {
			if (context?.previous) queryClient.setQueryData(key, context.previous);
		},
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: key });
			refreshCounts();
		},
	});

	const add = useMutation({
		meta: OWN_ERROR_TOAST,
		mutationFn: (targets: SourceTarget[]) => notebooksApi.addSources(notebookId, targets),
		onSuccess: (sources) => {
			queryClient.setQueryData(key, sources);
			refreshCounts();
		},
	});

	const remove = useMutation<unknown, Error, number, Snapshot>({
		meta: OWN_ERROR_TOAST,
		mutationFn: (sourceId) => notebooksApi.removeSource(notebookId, sourceId),
		onMutate: async (sourceId) => {
			await queryClient.cancelQueries({ queryKey: key });
			const previous = queryClient.getQueryData<NotebookSource[]>(key);
			if (previous) {
				queryClient.setQueryData(
					key,
					previous.filter((source) => source.id !== sourceId)
				);
			}
			return { previous };
		},
		onError: (_error, _vars, context) => {
			if (context?.previous) queryClient.setQueryData(key, context.previous);
		},
		onSettled: refreshCounts,
	});

	/** Re-read the list after sources were linked elsewhere (a connector import). */
	const refresh = () => {
		void queryClient.invalidateQueries({ queryKey: key });
		refreshCounts();
	};

	return { toggle, add, remove, refresh };
}

export function useStudioMutations(notebookId: number) {
	const queryClient = useQueryClient();
	const key = notebookKeys.outputs(notebookId);
	const refresh = () => queryClient.invalidateQueries({ queryKey: key });

	const generate = useMutation({
		meta: OWN_ERROR_TOAST,
		mutationFn: (body: StudioRequestBody) => notebooksApi.generate(notebookId, body),
		onSuccess: refresh,
	});
	const rename = useMutation({
		mutationFn: ({ outputId, title }: { outputId: number; title: string }) =>
			notebooksApi.renameOutput(notebookId, outputId, title),
		onSuccess: refresh,
	});
	const remove = useMutation({
		mutationFn: (outputId: number) => notebooksApi.deleteOutput(notebookId, outputId),
		onSuccess: refresh,
	});
	return { generate, rename, remove };
}
