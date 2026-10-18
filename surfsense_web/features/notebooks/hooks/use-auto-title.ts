"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useRef, useState } from "react";
import { notebookKeys } from "../api/notebook-query-keys";
import {
	readySourceCount,
	startTitleWatch,
	type TitleWatch,
	withNotebookName,
} from "../lib/auto-title";
import type { Notebook } from "../model/notebook";
import { useNotebook, useNotebookSources } from "./use-notebook-queries";

/**
 * The notebook page's notebook and sources, plus the automatic name: when a
 * source becomes ready (the sources query already polls while indexing) or
 * a chat turn ends (``watchTitle``), the notebook is re-read for a short
 * while until its new name arrives, and the name is copied into the home
 * list's cache. The old title stays painted until then; nothing flashes.
 */
export function useNotebookWithAutoTitle(workspaceId: number, notebookId: number) {
	const queryClient = useQueryClient();
	const [titleWatch, setTitleWatch] = useState<TitleWatch | null>(null);
	const notebook = useNotebook(workspaceId, notebookId, titleWatch);
	const sources = useNotebookSources(workspaceId, notebookId);

	const watchTitle = useCallback(() => {
		const current = queryClient.getQueryData<Notebook>(
			notebookKeys.detail(workspaceId, notebookId)
		);
		const next = startTitleWatch(current);
		if (!next) return;
		setTitleWatch(next);
		void queryClient.invalidateQueries({
			queryKey: notebookKeys.detail(workspaceId, notebookId),
			exact: true,
		});
	}, [queryClient, workspaceId, notebookId]);

	// A source that just finished indexing may have named the notebook.
	const ready = readySourceCount(sources.data);
	const previousReady = useRef<number | null>(null);
	useEffect(() => {
		const before = previousReady.current;
		previousReady.current = sources.data ? ready : before;
		if (before !== null && sources.data && ready > before) watchTitle();
	}, [ready, sources.data, watchTitle]);

	// Home and the sidebar read the list: give them the new name too.
	const data = notebook.data;
	useEffect(() => {
		if (!data) return;
		queryClient.setQueryData<Notebook[]>(notebookKeys.list(workspaceId), (list) =>
			withNotebookName(list, data)
		);
	}, [data, queryClient, workspaceId]);

	return { notebook, sources, watchTitle };
}
