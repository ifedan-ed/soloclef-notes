"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useCallback, useState } from "react";
import { notebookKeys } from "../api/notebook-query-keys";
import { type NotebookChanges, notebooksApi } from "../api/notebooks-api";
import { defaultNotebookIcon } from "../lib/relative-time";
import type { Notebook } from "../model/notebook";

/** Put a changed notebook into the detail and list caches. */
function useStoreNotebook(workspaceId: number) {
	const queryClient = useQueryClient();
	return useCallback(
		(notebook: Notebook) => {
			queryClient.setQueryData(notebookKeys.detail(workspaceId, notebook.id), notebook);
			queryClient.setQueryData<Notebook[]>(notebookKeys.list(workspaceId), (current) => {
				if (!current) return current;
				return current.some((item) => item.id === notebook.id)
					? current.map((item) => (item.id === notebook.id ? notebook : item))
					: [notebook, ...current];
			});
		},
		[queryClient, workspaceId]
	);
}

export function useUpdateNotebook(workspaceId: number) {
	const store = useStoreNotebook(workspaceId);
	return useCallback(
		async (notebookId: number, changes: NotebookChanges) => {
			const updated = await notebooksApi.update(workspaceId, notebookId, changes);
			store(updated);
			return updated;
		},
		[store, workspaceId]
	);
}

export function useSetFeatured(workspaceId: number) {
	const store = useStoreNotebook(workspaceId);
	return useCallback(
		async (notebookId: number, featured: boolean) => {
			const updated = await notebooksApi.setFeatured(workspaceId, notebookId, featured);
			store(updated);
			return updated;
		},
		[store, workspaceId]
	);
}

/** Copy a notebook and open the copy. */
export function useCopyNotebook(workspaceId: number) {
	const store = useStoreNotebook(workspaceId);
	const router = useRouter();
	const t = useTranslations("notebooks");
	return useCallback(
		async (notebook: Notebook) => {
			const copy = await notebooksApi.copy(
				workspaceId,
				notebook.id,
				t("copy_title", { title: notebook.title })
			);
			store(copy);
			router.push(`/dashboard/${workspaceId}/notebooks/${copy.id}`);
			return copy;
		},
		[router, store, t, workspaceId]
	);
}

/** Create an untitled notebook and open it; ``creating`` guards double clicks. */
export function useCreateNotebook(workspaceId: number) {
	const store = useStoreNotebook(workspaceId);
	const router = useRouter();
	const t = useTranslations("notebooks");
	const [creating, setCreating] = useState(false);
	const create = useCallback(async () => {
		if (creating) return;
		setCreating(true);
		try {
			const notebook = await notebooksApi.create(workspaceId, {
				title: t("untitled"),
				icon: defaultNotebookIcon(Date.now()),
			});
			store(notebook);
			router.push(`/dashboard/${workspaceId}/notebooks/${notebook.id}`);
		} catch (error) {
			setCreating(false);
			throw error;
		}
	}, [creating, router, store, t, workspaceId]);
	return { create, creating };
}
