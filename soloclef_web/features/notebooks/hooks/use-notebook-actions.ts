"use client";

import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "next/navigation";
import { useTranslations } from "next-intl";
import { useCallback, useState } from "react";
import { notebookPath } from "@/lib/dashboard-paths";
import { notebookKeys } from "../api/notebook-query-keys";
import { type NotebookChanges, notebooksApi } from "../api/notebooks-api";
import { defaultNotebookIcon } from "../lib/relative-time";
import type { Notebook } from "../model/notebook";

/** Put a changed notebook into the detail and list caches. */
function useStoreNotebook() {
	const queryClient = useQueryClient();
	return useCallback(
		(notebook: Notebook) => {
			queryClient.setQueryData(notebookKeys.detail(notebook.id), notebook);
			queryClient.setQueryData<Notebook[]>(notebookKeys.list(), (current) => {
				if (!current) return current;
				return current.some((item) => item.id === notebook.id)
					? current.map((item) => (item.id === notebook.id ? notebook : item))
					: [notebook, ...current];
			});
		},
		[queryClient]
	);
}

export function useUpdateNotebook() {
	const store = useStoreNotebook();
	return useCallback(
		async (notebookId: number, changes: NotebookChanges) => {
			const updated = await notebooksApi.update(notebookId, changes);
			store(updated);
			return updated;
		},
		[store]
	);
}

export function useSetFeatured() {
	const store = useStoreNotebook();
	return useCallback(
		async (notebookId: number, featured: boolean) => {
			const updated = await notebooksApi.setFeatured(notebookId, featured);
			store(updated);
			return updated;
		},
		[store]
	);
}

/** Copy a notebook and open the copy. */
export function useCopyNotebook() {
	const store = useStoreNotebook();
	const router = useRouter();
	const t = useTranslations("notebooks");
	return useCallback(
		async (notebook: Notebook) => {
			const copy = await notebooksApi.copy(notebook.id, t("copy_title", { title: notebook.title }));
			store(copy);
			router.push(notebookPath(copy.id));
			return copy;
		},
		[router, store, t]
	);
}

/** Create an untitled notebook and open it; ``creating`` guards double clicks. */
export function useCreateNotebook() {
	const store = useStoreNotebook();
	const router = useRouter();
	const t = useTranslations("notebooks");
	const [creating, setCreating] = useState(false);
	const create = useCallback(async () => {
		if (creating) return;
		setCreating(true);
		try {
			const notebook = await notebooksApi.create({
				title: t("untitled"),
				icon: defaultNotebookIcon(Date.now()),
			});
			store(notebook);
			router.push(notebookPath(notebook.id));
		} catch (error) {
			setCreating(false);
			throw error;
		}
	}, [creating, router, store, t]);
	return { create, creating };
}
