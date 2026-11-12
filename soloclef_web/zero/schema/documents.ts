import { boolean, json, number, string, table } from "@rocicorp/zero";

export const documentTable = table("documents")
	.columns({
		id: number(),
		title: string(),
		documentType: string().from("document_type"),
		userId: string().from("user_id"),
		folderId: number().optional().from("folder_id"),
		status: json(),
		createdAt: number().from("created_at"),
		updatedAt: number().from("updated_at"),
	})
	.primaryKey("id");

// No config column: it holds a connector's keys and tokens, and nothing that
// reaches a browser by replication may carry them. The panel takes config
// from the REST list, which masks them.
export const searchSourceConnectorTable = table("search_source_connectors")
	.columns({
		id: number(),
		name: string(),
		connectorType: string().from("connector_type"),
		enabled: boolean(),
		isIndexable: boolean().from("is_indexable"),
		lastIndexedAt: number().optional().from("last_indexed_at"),
		periodicIndexingEnabled: boolean().from("periodic_indexing_enabled"),
		indexingFrequencyMinutes: number().optional().from("indexing_frequency_minutes"),
		nextScheduledAt: number().optional().from("next_scheduled_at"),
		userId: string().from("user_id"),
		createdAt: number().from("created_at"),
	})
	.primaryKey("id");
