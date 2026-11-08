"use client";

import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

/**
 * For mutations whose callers show their own, worded error toast: without
 * this the global mutation handler shows a second toast for the same failure.
 */
const OWN_ERROR_TOAST = { suppressGlobalErrorToast: true } as const;

import { notebookKeys } from "../api/notebook-query-keys";
import { notebooksApi, type SourceTarget } from "../api/notebooks-api";
import {
	notebookRefetchInterval,
	notebooksRefetchInterval,
	type TitleWatch,
} from "../lib/auto-title";
import { noteSavesSettled, saveNotebookNote } from "../lib/note-saves";
import { outputsRefetchInterval } from "../lib/output-meta";
import { withToggled } from "../lib/source-selection";
import type { StudioRequestBody } from "../lib/studio-request";
import type { Notebook, NotebookNote, NotebookSource } from "../model/notebook";

export function useNotebooks(workspaceId: number) {
	return useQuery({
		queryKey: notebookKeys.list(workspaceId),
		queryFn: () => notebooksApi.list(workspaceId),
		enabled: workspaceId > 0,
		placeholderData: keepPreviousData,
		// A notebook about to name itself: look again until its name lands.
		refetchInterval: (query) => notebooksRefetchInterval(query.state.data),
	});
}

export function useNotebook(
	workspaceId: number,
	notebookId: number,
	titleWatch: TitleWatch | null = null
) {
	const queryClient = useQueryClient();
	return useQuery({
		queryKey: notebookKeys.detail(workspaceId, notebookId),
		queryFn: () => notebooksApi.get(workspaceId, notebookId),
		enabled: workspaceId > 0 && notebookId > 0,
		// Its automatic name is on the way (a source just became ready, or a
		// chat turn ended): look again, briefly, until it arrives.
		refetchInterval: (query) => notebookRefetchInterval(query.state.data, titleWatch),
		// Arriving from the list: paint the card's data at once, refresh behind it.
		placeholderData: () =>
			queryClient
				.getQueryData<Notebook[]>(notebookKeys.list(workspaceId))
				?.find((notebook) => notebook.id === notebookId),
	});
}

export function useNotebookSources(workspaceId: number, notebookId: number) {
	return useQuery({
		queryKey: notebookKeys.sources(workspaceId, notebookId),
		queryFn: () => notebooksApi.sources(workspaceId, notebookId),
		enabled: workspaceId > 0 && notebookId > 0,
		// Sources still being indexed flip to ready on their own.
		refetchInterval: (query) =>
			query.state.data?.some((s) => s.status === "pending" || s.status === "processing")
				? 5000
				: false,
	});
}

export function useNotebookNotes(workspaceId: number, notebookId: number) {
	return useQuery({
		queryKey: notebookKeys.notes(workspaceId, notebookId),
		// Asked while a save is on its way, a read would bring back the text from before it.
		queryFn: async () => {
			await noteSavesSettled(workspaceId, notebookId);
			return notebooksApi.notes(workspaceId, notebookId);
		},
		enabled: workspaceId > 0 && notebookId > 0,
	});
}

export function useNotebookThreads(workspaceId: number, notebookId: number) {
	return useQuery({
		queryKey: notebookKeys.threads(workspaceId, notebookId),
		queryFn: () => notebooksApi.threads(workspaceId, notebookId),
		enabled: workspaceId > 0 && notebookId > 0,
	});
}

export function useStudioOutputs(workspaceId: number, notebookId: number) {
	return useQuery({
		queryKey: notebookKeys.outputs(workspaceId, notebookId),
		queryFn: () => notebooksApi.outputs(workspaceId, notebookId),
		enabled: workspaceId > 0 && notebookId > 0,
		placeholderData: keepPreviousData,
		refetchInterval: (query) =>
			outputsRefetchInterval((query.state.data ?? []).map((output) => output.status)),
	});
}

export function useSourceGuide(workspaceId: number, notebookId: number, sourceId: number | null) {
	return useQuery({
		queryKey: notebookKeys.guide(workspaceId, notebookId, sourceId ?? 0),
		queryFn: () => notebooksApi.sourceGuide(workspaceId, notebookId, sourceId ?? 0),
		enabled: sourceId != null,
		staleTime: 5 * 60_000,
	});
}

export function useStudioSuggestions(
	workspaceId: number,
	notebookId: number,
	kind: string,
	{ variant = "", language = "" }: { variant?: string; language?: string },
	enabled: boolean
) {
	return useQuery({
		queryKey: notebookKeys.suggestions(workspaceId, notebookId, kind, variant, language),
		queryFn: () => notebooksApi.suggestions(workspaceId, notebookId, kind, { variant, language }),
		enabled,
		staleTime: 10 * 60_000,
		// Switching format or language keeps the last ideas painted until the
		// new ones arrive, instead of blanking the chips.
		placeholderData: keepPreviousData,
	});
}

export function useReportTemplates(workspaceId: number, enabled: boolean) {
	return useQuery({
		queryKey: notebookKeys.reportTemplates(workspaceId),
		queryFn: () => notebooksApi.reportTemplates(workspaceId),
		enabled,
	});
}

/** Source mutations, applied optimistically so checkboxes answer instantly. */
export function useSourceMutations(workspaceId: number, notebookId: number) {
	const queryClient = useQueryClient();
	const key = notebookKeys.sources(workspaceId, notebookId);
	const refreshCounts = () => {
		void queryClient.invalidateQueries({ queryKey: notebookKeys.detail(workspaceId, notebookId) });
		void queryClient.invalidateQueries({ queryKey: notebookKeys.list(workspaceId) });
	};

	type ToggleVars = { sourceId: number | "all"; enabled: boolean };
	type Snapshot = { previous?: NotebookSource[] };

	const toggle = useMutation<unknown, Error, ToggleVars, Snapshot>({
		meta: OWN_ERROR_TOAST,
		mutationFn: async ({ sourceId, enabled }) => {
			if (sourceId === "all") {
				await notebooksApi.toggleAllSources(workspaceId, notebookId, enabled);
			} else {
				await notebooksApi.toggleSource(workspaceId, notebookId, sourceId, enabled);
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
		mutationFn: (targets: SourceTarget[]) =>
			notebooksApi.addSources(workspaceId, notebookId, targets),
		onSuccess: (sources) => {
			queryClient.setQueryData(key, sources);
			refreshCounts();
		},
	});

	const remove = useMutation<unknown, Error, number, Snapshot>({
		meta: OWN_ERROR_TOAST,
		mutationFn: (sourceId) => notebooksApi.removeSource(workspaceId, notebookId, sourceId),
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

export function useNoteMutations(workspaceId: number, notebookId: number) {
	const queryClient = useQueryClient();
	const key = notebookKeys.notes(workspaceId, notebookId);
	const upsert = (note: NotebookNote) =>
		queryClient.setQueryData<NotebookNote[]>(key, (current = []) => [
			note,
			...current.filter((existing) => existing.id !== note.id),
		]);

	const create = useMutation({
		meta: OWN_ERROR_TOAST,
		mutationFn: (body: { title: string; content: string }) =>
			notebooksApi.createNote(workspaceId, notebookId, body),
		onSuccess: upsert,
	});
	// Both of the note's caches take the text as the save starts (lib/note-saves.ts).
	const update = useMutation({
		meta: OWN_ERROR_TOAST,
		mutationFn: ({ noteId, ...body }: { noteId: number; title?: string; content?: string }) =>
			saveNotebookNote(queryClient, { workspaceId, notebookId, noteId }, body, (changes) =>
				notebooksApi.updateNote(workspaceId, notebookId, noteId, changes)
			),
	});
	const remove = useMutation({
		meta: OWN_ERROR_TOAST,
		mutationFn: (noteId: number) => notebooksApi.deleteNote(workspaceId, notebookId, noteId),
		onSuccess: (_result, noteId) =>
			queryClient.setQueryData<NotebookNote[]>(key, (current = []) =>
				current.filter((note) => note.id !== noteId)
			),
	});
	const convert = useMutation({
		meta: OWN_ERROR_TOAST,
		mutationFn: (noteId: number) => notebooksApi.convertNote(workspaceId, notebookId, noteId),
		onSuccess: (sources) => {
			queryClient.setQueryData(notebookKeys.sources(workspaceId, notebookId), sources);
			void queryClient.invalidateQueries({ queryKey: key });
			void queryClient.invalidateQueries({
				queryKey: notebookKeys.detail(workspaceId, notebookId),
			});
		},
	});
	return { create, update, remove, convert };
}

export function useStudioMutations(workspaceId: number, notebookId: number) {
	const queryClient = useQueryClient();
	const key = notebookKeys.outputs(workspaceId, notebookId);
	const refresh = () => queryClient.invalidateQueries({ queryKey: key });

	const generate = useMutation({
		meta: OWN_ERROR_TOAST,
		mutationFn: (body: StudioRequestBody) => notebooksApi.generate(workspaceId, notebookId, body),
		onSuccess: refresh,
	});
	const rename = useMutation({
		mutationFn: ({ outputId, title }: { outputId: number; title: string }) =>
			notebooksApi.renameOutput(workspaceId, notebookId, outputId, title),
		onSuccess: refresh,
	});
	const remove = useMutation({
		mutationFn: (outputId: number) => notebooksApi.deleteOutput(workspaceId, notebookId, outputId),
		onSuccess: refresh,
	});
	return { generate, rename, remove };
}
