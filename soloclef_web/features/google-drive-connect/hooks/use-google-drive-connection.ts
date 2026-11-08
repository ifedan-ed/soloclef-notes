"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { googleDriveApi } from "../api/google-drive-api";

export const googleDriveConnectionKey = (workspaceId: number) =>
	["google-drive-connection", workspaceId] as const;

/** This person's own Google Drive connection in the workspace. */
export function useGoogleDriveConnection(workspaceId: number) {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: googleDriveConnectionKey(workspaceId),
		queryFn: () => googleDriveApi.connection(workspaceId),
		staleTime: 60_000,
	});
	const refresh = useCallback(async () => {
		await Promise.all([
			queryClient.invalidateQueries({ queryKey: googleDriveConnectionKey(workspaceId) }),
			queryClient.invalidateQueries({ queryKey: cacheKeys.connectors.all(String(workspaceId)) }),
		]);
	}, [queryClient, workspaceId]);
	const connection = query.data;
	return {
		connection,
		isLoading: query.isLoading,
		isConnected: connection?.connector_id != null,
		refresh,
	};
}
