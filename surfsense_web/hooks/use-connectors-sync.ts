"use client";

import { useQuery } from "@rocicorp/zero/react";
import { useMemo } from "react";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";
import { queries } from "@/zero/queries";

/**
 * Syncs connectors for a workspace via Zero.
 * Returns connectors, loading state, error, and a refresh function.
 */
export function useConnectorsSync(workspaceId: number | string | null) {
	const spaceId = workspaceId ? Number(workspaceId) : -1;

	const [data, result] = useQuery(queries.connectors.bySpace({ workspaceId: spaceId }));

	const connectors: SearchSourceConnector[] = useMemo(() => {
		if (!workspaceId || !data) return [];
		return data.map((c) => ({
			id: c.id,
			name: c.name,
			connector_type: c.connectorType as SearchSourceConnector["connector_type"],
			is_indexable: c.isIndexable,
			is_active: true,
			last_indexed_at: c.lastIndexedAt ? new Date(c.lastIndexedAt).toISOString() : null,
			// Never synced (it holds the connector's keys): the panel takes the
			// REST list's masked copy (workspaceConnectorList), and an edit view
			// the server's own (editable-connector.ts).
			config: {},
			// Not synced either; the REST copy's, as config.
			enable_vision_llm: false,
			periodic_indexing_enabled: c.periodicIndexingEnabled,
			indexing_frequency_minutes: c.indexingFrequencyMinutes ?? null,
			next_scheduled_at: c.nextScheduledAt ? new Date(c.nextScheduledAt).toISOString() : null,
			workspace_id: c.workspaceId,
			user_id: c.userId,
			created_at: c.createdAt ? new Date(c.createdAt).toISOString() : new Date().toISOString(),
		}));
	}, [workspaceId, data]);

	const loading = !workspaceId ? false : result.type !== "complete";
	const error = !workspaceId ? null : null;

	const refreshConnectors = async () => {};

	return { connectors, loading, error, refreshConnectors };
}
