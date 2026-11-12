"use client";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { type NextcloudConnection, nextcloudConnectApi } from "../api/nextcloud-connect-api";

export const nextcloudConnectionKey = () => ["nextcloud-connection"] as const;

/** This person's own Nextcloud connection, and the server it uses. */
export function useNextcloudConnection() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: nextcloudConnectionKey(),
		queryFn: () => nextcloudConnectApi.connection(),
		staleTime: 60_000,
	});
	const refresh = useCallback(
		async (next?: NextcloudConnection | null) => {
			if (next) queryClient.setQueryData(nextcloudConnectionKey(), next);
			await Promise.all([
				queryClient.invalidateQueries({ queryKey: nextcloudConnectionKey() }),
				// Every connector list: the prefix all of their keys share, so a
				// new Nextcloud connection shows wherever connectors are listed.
				queryClient.invalidateQueries({ queryKey: ["connectors"] }),
			]);
		},
		[queryClient]
	);
	const connection = query.data;
	return {
		connection,
		isLoading: query.isLoading,
		isConnected: connection?.connector_id != null,
		refresh,
	};
}
