"use client";

import { keepPreviousData, useQuery } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { nextcloudFilesApi } from "../api/nextcloud-files-api";

const keys = {
	folder: (connectorId: number, path: string) =>
		["nextcloud-files", connectorId, "folder", path] as const,
	search: (connectorId: number, query: string) =>
		["nextcloud-files", connectorId, "search", query] as const,
};

function useDebounced<T>(value: T, delayMs: number): T {
	const [debounced, setDebounced] = useState(value);
	useEffect(() => {
		const timer = window.setTimeout(() => setDebounced(value), delayMs);
		return () => window.clearTimeout(timer);
	}, [value, delayMs]);
	return debounced;
}

/**
 * The open folder's entries, or name-search results while a query of two or
 * more characters is typed. The previous listing stays on screen until the
 * next one arrives, so navigating never blanks the list.
 */
export function useNextcloudListing(connectorId: number, path: string, query: string) {
	const term = useDebounced(query.trim(), 300);
	const searching = term.length >= 2;
	const folder = useQuery({
		queryKey: keys.folder(connectorId, path),
		queryFn: ({ signal }) => nextcloudFilesApi.browse(connectorId, path, signal),
		enabled: !searching,
		placeholderData: keepPreviousData,
		staleTime: 60 * 1000,
		retry: false,
	});
	const search = useQuery({
		queryKey: keys.search(connectorId, term),
		queryFn: ({ signal }) => nextcloudFilesApi.search(connectorId, term, signal),
		enabled: searching,
		placeholderData: keepPreviousData,
		staleTime: 60 * 1000,
		retry: false,
	});
	const active = searching ? search : folder;
	return {
		searching,
		entries: active.data?.entries ?? [],
		isFetching: active.isFetching,
		isLoading: active.isLoading,
		error: active.error,
	};
}
