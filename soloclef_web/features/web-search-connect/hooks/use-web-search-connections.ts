"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { type WebSearchConnection, webSearchConnectApi } from "../api/web-search-connect-api";
import { isResearchSourcesKey, type WebSearchProvider } from "../model/web-search-providers";

const LIST_KEY = ["web-search-connections"] as const;

/**
 * The account's Exa and Tavily keys (masked), and the two writes. After
 * either, the research box's status is asked again in every notebook, so
 * "Not connected" and the chat's Exa/Tavily tools follow at once.
 */
export function useWebSearchConnections() {
	const client = useQueryClient();
	const { data, isLoading } = useQuery({
		queryKey: LIST_KEY,
		queryFn: webSearchConnectApi.list,
		staleTime: 60_000,
	});

	const settle = useCallback(async () => {
		await Promise.all([
			client.invalidateQueries({ queryKey: LIST_KEY }),
			client.invalidateQueries({ predicate: (query) => isResearchSourcesKey(query.queryKey) }),
		]);
	}, [client]);

	const connect = useMutation({
		mutationFn: ({ provider, apiKey }: { provider: WebSearchProvider; apiKey: string }) =>
			webSearchConnectApi.connect(provider, apiKey),
		// The card says why a key was refused, under its field.
		meta: { suppressGlobalErrorToast: true },
		onSuccess: (saved: WebSearchConnection) => {
			client.setQueryData<WebSearchConnection[]>(LIST_KEY, (list) =>
				list ? list.map((row) => (row.provider === saved.provider ? saved : row)) : [saved]
			);
			void settle();
		},
	});

	const disconnect = useMutation({
		mutationFn: (provider: WebSearchProvider) => webSearchConnectApi.disconnect(provider),
		onSettled: () => void settle(),
	});

	return { connections: data, loading: isLoading, connect, disconnect };
}
