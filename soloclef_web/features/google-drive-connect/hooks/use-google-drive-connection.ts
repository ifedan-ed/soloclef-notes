"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { googleDriveApi } from "../api/google-drive-api";

/** One connection per account, so one key. */
export const googleDriveConnectionKey = () => ["google-drive-connection"] as const;

/** The account's own Google Drive connection. */
export function useGoogleDriveConnection() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: googleDriveConnectionKey(),
		queryFn: () => googleDriveApi.connection(),
		staleTime: 60_000,
	});
	const refresh = useCallback(async () => {
		await Promise.all([
			queryClient.invalidateQueries({ queryKey: googleDriveConnectionKey() }),
			queryClient.invalidateQueries({ queryKey: cacheKeys.connectors.all() }),
		]);
	}, [queryClient]);
	const connection = query.data;
	return {
		connection,
		isLoading: query.isLoading,
		isConnected: connection?.connector_id != null,
		refresh,
	};
}
