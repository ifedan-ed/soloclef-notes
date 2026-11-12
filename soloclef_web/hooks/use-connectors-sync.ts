"use client";

import { useQuery } from "@rocicorp/zero/react";
import { useMemo } from "react";
import type { SearchSourceConnector } from "@/contracts/types/connector.types";
import { queries } from "@/zero/queries";

/**
 * The account's connectors as Zero syncs them, so the connectors panel
 * notices a change (a sync finishing, a connection added on the phone)
 * without polling. Zero's context is the signed-in account, so the query
 * takes no argument: it can only ever return this account's rows.
 */
export function useConnectorsSync() {
	const [data, result] = useQuery(queries.connectors.mine({}));

	// Zero syncs neither `enabled` nor the config, so these rows are not whole
	// connectors: the connectors panel uses them only to notice a change.
	const connectors: Omit<SearchSourceConnector, "enabled">[] = useMemo(() => {
		if (!data) return [];
		return data.map((c) => ({
			id: c.id,
			name: c.name,
			connector_type: c.connectorType as SearchSourceConnector["connector_type"],
			is_indexable: c.isIndexable,
			is_active: true,
			last_indexed_at: c.lastIndexedAt ? new Date(c.lastIndexedAt).toISOString() : null,
			// Never synced (it holds the connector's keys).
			config: {},
			// Not synced either.
			enable_vision_llm: false,
			periodic_indexing_enabled: c.periodicIndexingEnabled,
			indexing_frequency_minutes: c.indexingFrequencyMinutes ?? null,
			next_scheduled_at: c.nextScheduledAt ? new Date(c.nextScheduledAt).toISOString() : null,
			user_id: c.userId,
			created_at: c.createdAt ? new Date(c.createdAt).toISOString() : new Date().toISOString(),
		}));
	}, [data]);

	const loading = result.type !== "complete";

	const refreshConnectors = async () => {};

	return { connectors, loading, error: null, refreshConnectors };
}
