/**
 * TanStack Query hooks, keyed like the web's (features/notebooks/api/
 * notebook-query-keys.ts) and polling where the web polls. Lists keep the
 * previous data painted while they refetch (no loading flashes).
 */
import { keepPreviousData, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import type { SourceTarget } from "./api/endpoints";
import type { Notebook, NotebookSource, StudioOutput } from "./api/schemas";
import { useApp } from "./app-context";
import { outputsRefetchInterval } from "./studio/catalog";
import { CONTENT_SEARCH_DEBOUNCE_MS, contentSearchQuery, isIndexing, withToggled } from "./util/notebooks";
import { linkSource } from "./util/uploads";

export const keys = {
	me: ["me"] as const,
	workspaces: ["workspaces"] as const,
	notebooks: (ws: number) => ["notebooks", ws] as const,
	search: (ws: number, query: string) => ["notebook-search", ws, query] as const,
	notebook: (ws: number, nb: number) => ["notebooks", ws, nb] as const,
	sources: (ws: number, nb: number) => ["notebooks", ws, nb, "sources"] as const,
	guide: (ws: number, nb: number, sourceId: number) => ["notebooks", ws, nb, "guide", sourceId] as const,
	threads: (ws: number, nb: number) => ["notebooks", ws, nb, "threads"] as const,
	history: (threadId: number) => ["threads", threadId, "messages"] as const,
	outputs: (ws: number, nb: number) => ["notebooks", ws, nb, "outputs"] as const,
	suggestions: (ws: number, nb: number, kind: string, variant = "", language = "") =>
		["notebooks", ws, nb, "suggestions", kind, variant, language] as const,
	followups: (ws: number, nb: number, messageKey: string) => ["notebooks", ws, nb, "followups", messageKey] as const,
	reportTemplates: (ws: number) => ["notebook-report-templates", ws] as const,
	etl: ["etl-capabilities"] as const,
	podcastLanguages: ["podcast-languages"] as const,
	manifest: (ws: number, artifactId: number) => ["artifact-manifest", ws, artifactId] as const,
	chunk: (chunkId: number) => ["chunk", chunkId] as const,
	billing: (locale: string) => ["billing-me", locale] as const,
};

export function useMe() {
	const { ep, auth } = useApp();
	return useQuery({ queryKey: keys.me, queryFn: ep.me, enabled: auth === "signedIn", staleTime: 5 * 60_000 });
}

/** The signed-in person's plan and credit balances (Settings); fails quietly on servers without billing. */
export function useBilling() {
	const { ep, auth, locale } = useApp();
	return useQuery({
		queryKey: keys.billing(locale),
		queryFn: () => ep.billingMe(locale),
		enabled: auth === "signedIn",
		staleTime: 60_000,
		retry: false,
	});
}

/** The workspace notebooks live in: the one chosen in Settings, else the first. */
export function useWorkspace() {
	const { ep, auth, prefs } = useApp();
	const query = useQuery({
		queryKey: keys.workspaces,
		queryFn: ep.workspaces,
		enabled: auth === "signedIn",
		staleTime: 5 * 60_000,
	});
	const list = query.data ?? [];
	const chosen = list.find((w) => w.id === prefs.workspaceId) ?? list[0];
	return { workspaceId: chosen?.id ?? 0, workspace: chosen, workspaces: list, query };
}

export function useNotebooks(ws: number) {
	const { ep } = useApp();
	return useQuery({
		queryKey: keys.notebooks(ws),
		queryFn: () => ep.notebooks(ws),
		enabled: ws > 0,
		placeholderData: keepPreviousData,
	});
}

/**
 * The home's "Found inside notebooks" search (web: hooks/use-content-search.ts):
 * sent once typing pauses, with the previous results kept on screen meanwhile.
 */
export function useContentSearch(ws: number, raw: string) {
	const { ep } = useApp();
	const current = contentSearchQuery(raw);
	const [sent, setSent] = useState(current);
	useEffect(() => {
		const timer = setTimeout(() => setSent(current), CONTENT_SEARCH_DEBOUNCE_MS);
		return () => clearTimeout(timer);
	}, [current]);
	const query = useQuery({
		queryKey: keys.search(ws, sent),
		queryFn: () => ep.searchNotebooks(ws, sent),
		enabled: ws > 0 && sent !== "",
		placeholderData: keepPreviousData,
		staleTime: 30_000,
		retry: 1,
	});
	const active = current !== "";
	return {
		active,
		query: current,
		hits: active ? (query.data?.notebooks ?? []) : [],
		pending: active && (sent !== current || query.isFetching),
		settled: active && sent === current && !!query.data && !query.isPlaceholderData,
		failed: active && sent === current && query.isError && !query.data,
	};
}

export function useNotebook(ws: number, nb: number) {
	const { ep } = useApp();
	const queryClient = useQueryClient();
	return useQuery({
		queryKey: keys.notebook(ws, nb),
		queryFn: () => ep.notebook(ws, nb),
		enabled: ws > 0 && nb > 0,
		// Arriving from the list: paint the card's data at once, refresh behind it.
		placeholderData: () => queryClient.getQueryData<Notebook[]>(keys.notebooks(ws))?.find((n) => n.id === nb),
	});
}

export function useSources(ws: number, nb: number) {
	const { ep } = useApp();
	return useQuery({
		queryKey: keys.sources(ws, nb),
		queryFn: () => ep.sources(ws, nb),
		enabled: ws > 0 && nb > 0,
		refetchInterval: (query) => (query.state.data?.some(isIndexing) ? 5000 : false),
	});
}

export function useOutputs(ws: number, nb: number) {
	const { ep } = useApp();
	return useQuery({
		queryKey: keys.outputs(ws, nb),
		queryFn: () => ep.outputs(ws, nb),
		enabled: ws > 0 && nb > 0,
		placeholderData: keepPreviousData,
		refetchInterval: (query) => outputsRefetchInterval((query.state.data ?? []).map((output) => output.status)),
	});
}

export function useSourceGuide(ws: number, nb: number, sourceId: number) {
	const { ep } = useApp();
	return useQuery({
		queryKey: keys.guide(ws, nb, sourceId),
		queryFn: () => ep.sourceGuide(ws, nb, sourceId),
		enabled: ws > 0 && nb > 0 && sourceId > 0,
		staleTime: 5 * 60_000,
	});
}

export function useSuggestions(
	ws: number,
	nb: number,
	kind: string,
	{ variant = "", language = "" }: { variant?: string; language?: string },
	enabled: boolean
) {
	const { ep } = useApp();
	return useQuery({
		queryKey: keys.suggestions(ws, nb, kind, variant, language),
		queryFn: () => ep.suggestions(ws, nb, kind, { variant, language }),
		enabled: enabled && ws > 0 && nb > 0,
		staleTime: 10 * 60_000,
		placeholderData: keepPreviousData,
		retry: false,
	});
}

export function useEtlCapabilities(enabled = true) {
	const { ep } = useApp();
	return useQuery({ queryKey: keys.etl, queryFn: ep.etlCapabilities, enabled, staleTime: 10 * 60_000 });
}

export function useReportTemplates(ws: number, enabled: boolean) {
	const { ep } = useApp();
	return useQuery({
		queryKey: keys.reportTemplates(ws),
		queryFn: () => ep.reportTemplates(ws),
		enabled: enabled && ws > 0,
	});
}

export function usePodcastLanguages(enabled: boolean) {
	const { ep } = useApp();
	return useQuery({
		queryKey: keys.podcastLanguages,
		queryFn: ep.podcastLanguages,
		enabled,
		staleTime: 10 * 60_000,
	});
}

export function useManifest(ws: number, artifactId: number | null | undefined) {
	const { ep } = useApp();
	return useQuery({
		queryKey: keys.manifest(ws, artifactId ?? 0),
		queryFn: () => ep.artifactManifest(ws, artifactId ?? 0),
		enabled: ws > 0 && !!artifactId,
	});
}

/** Notebook create / rename / delete, updating the home list in place. */
export function useNotebookMutations(ws: number) {
	const { ep } = useApp();
	const queryClient = useQueryClient();
	const listKey = keys.notebooks(ws);
	const upsert = (notebook: Notebook) =>
		queryClient.setQueryData<Notebook[]>(listKey, (current = []) => [
			notebook,
			...current.filter((n) => n.id !== notebook.id),
		]);
	const create = useMutation({
		mutationFn: (body: { title: string; icon?: string | null }) => ep.createNotebook(ws, body),
		onSuccess: upsert,
	});
	const rename = useMutation({
		mutationFn: ({ id, title }: { id: number; title: string }) => ep.updateNotebook(ws, id, { title }),
		onSuccess: (notebook) => {
			upsert(notebook);
			queryClient.setQueryData(keys.notebook(ws, notebook.id), notebook);
		},
	});
	const remove = useMutation({
		mutationFn: (id: number) => ep.deleteNotebook(ws, id),
		onSuccess: (_result, id) =>
			queryClient.setQueryData<Notebook[]>(listKey, (current = []) => current.filter((n) => n.id !== id)),
	});
	return { create, rename, remove };
}

/** Source toggles and removal, applied optimistically as on the web. */
export function useSourceMutations(ws: number, nb: number) {
	const { ep } = useApp();
	const queryClient = useQueryClient();
	const key = keys.sources(ws, nb);
	const refreshCounts = () => {
		void queryClient.invalidateQueries({ queryKey: keys.notebook(ws, nb), exact: true });
		void queryClient.invalidateQueries({ queryKey: keys.notebooks(ws), exact: true });
	};
	type Snapshot = { previous?: NotebookSource[] };

	const toggle = useMutation<unknown, Error, { sourceId: number | "all"; enabled: boolean }, Snapshot>({
		mutationFn: ({ sourceId, enabled }) =>
			sourceId === "all" ? ep.toggleAllSources(ws, nb, enabled) : ep.toggleSource(ws, nb, sourceId, enabled),
		onMutate: async ({ sourceId, enabled }) => {
			await queryClient.cancelQueries({ queryKey: key });
			const previous = queryClient.getQueryData<NotebookSource[]>(key);
			if (previous) queryClient.setQueryData(key, withToggled(previous, sourceId, enabled));
			return { previous };
		},
		onError: (_e, _v, context) => {
			if (context?.previous) queryClient.setQueryData(key, context.previous);
		},
		onSettled: () => {
			void queryClient.invalidateQueries({ queryKey: key });
			refreshCounts();
		},
	});

	const add = useMutation({
		mutationFn: (targets: SourceTarget[]) => ep.addSources(ws, nb, targets),
		onSuccess: (sources) => {
			queryClient.setQueryData(key, sources);
			refreshCounts();
		},
	});

	const importLink = useMutation({
		mutationFn: (url: string) => ep.importLinks(ws, nb, [{ url, title: url, source: linkSource(url) }]),
		onSuccess: (sources) => {
			queryClient.setQueryData(key, sources);
			refreshCounts();
		},
	});

	const remove = useMutation<unknown, Error, number, Snapshot>({
		mutationFn: (sourceId) => ep.removeSource(ws, nb, sourceId),
		onMutate: async (sourceId) => {
			await queryClient.cancelQueries({ queryKey: key });
			const previous = queryClient.getQueryData<NotebookSource[]>(key);
			if (previous)
				queryClient.setQueryData(
					key,
					previous.filter((s) => s.id !== sourceId)
				);
			return { previous };
		},
		onError: (_e, _v, context) => {
			if (context?.previous) queryClient.setQueryData(key, context.previous);
		},
		onSettled: refreshCounts,
	});

	return { toggle, add, importLink, remove };
}

export function useStudioMutations(ws: number, nb: number) {
	const { ep } = useApp();
	const queryClient = useQueryClient();
	const key = keys.outputs(ws, nb);
	const refresh = () => queryClient.invalidateQueries({ queryKey: key });
	const generate = useMutation({
		mutationFn: (body: Record<string, unknown>) => ep.generate(ws, nb, body),
		onSuccess: (output) => {
			queryClient.setQueryData<StudioOutput[]>(key, (current = []) => [
				output,
				...current.filter((o) => o.id !== output.id),
			]);
			void refresh();
		},
	});
	const rename = useMutation({
		mutationFn: ({ outputId, title }: { outputId: number; title: string }) => ep.renameOutput(ws, nb, outputId, title),
		onSuccess: refresh,
	});
	const remove = useMutation({
		mutationFn: (outputId: number) => ep.deleteOutput(ws, nb, outputId),
		onMutate: (outputId) =>
			queryClient.setQueryData<StudioOutput[]>(key, (current = []) => current.filter((o) => o.id !== outputId)),
		onSettled: refresh,
	});
	return { generate, rename, remove };
}
