"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { cacheKeys } from "@/lib/query-client/cache-keys";
import { type NextcloudConnection, nextcloudConnectApi } from "../api/nextcloud-connect-api";

export const nextcloudConnectionKey = (workspaceId: number) =>
	["nextcloud-connection", workspaceId] as const;

/** This person's own Nextcloud connection in the workspace, and the server it uses. */
export function useNextcloudConnection(workspaceId: number) {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: nextcloudConnectionKey(workspaceId),
		queryFn: () => nextcloudConnectApi.connection(workspaceId),
		staleTime: 60_000,
	});
	const refresh = useCallback(
		async (next?: NextcloudConnection | null) => {
			if (next) queryClient.setQueryData(nextcloudConnectionKey(workspaceId), next);
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: nextcloudConnectionKey(workspaceId) }),
				queryClient.invalidateQueries({
					queryKey: cacheKeys.connectors.all(String(workspaceId)),
				}),
			]);
		},
		[queryClient, workspaceId]
	);
	const connection = query.data;
	return {
		connection,
		isLoading: query.isLoading,
		isConnected: connection?.connector_id != null,
		refresh,
	};
}
