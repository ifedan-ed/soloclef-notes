import { z } from "zod";
import { paginationQueryParams } from ".";

export const searchSourceConnectorTypeEnum = z.enum([
	"GOOGLE_DRIVE_CONNECTOR",
	"WEBCRAWLER_CONNECTOR",
	"NEXTCLOUD_CONNECTOR",
	"MCP_CONNECTOR",
	"EMAIL_IMAP_CONNECTOR",
]);

export const searchSourceConnector = z.object({
	id: z.number(),
	name: z.string(),
	connector_type: searchSourceConnectorTypeEnum,
	is_indexable: z.boolean(),
	is_active: z.boolean().default(true),
	last_indexed_at: z.string().nullable(),
	config: z.record(z.string(), z.any()),
	enable_vision_llm: z.boolean().default(false),
	periodic_indexing_enabled: z.boolean(),
	indexing_frequency_minutes: z.number().nullable(),
	next_scheduled_at: z.string().nullable(),
	// NULL for every connector type but MCP: account-level connections have
	// no single workspace (custom/ACCOUNT_CONNECTIONS.md).
	workspace_id: z.number().nullable(),
	user_id: z.string(),
	created_at: z.string(),
	// Set by the list endpoint only: whether this connection is switched on
	// in the workspace that was asked about, and whether the caller owns it
	// (only the owner may see/use the on-off switch).
	enabled_in_workspace: z.boolean().nullable().optional(),
	is_mine: z.boolean().nullable().optional(),
});

/**
 * Get connectors
 */
export const getConnectorsRequest = z.object({
	queryParams: paginationQueryParams
		.pick({ skip: true, limit: true })
		.extend({
			workspace_id: z.number().or(z.string()).nullish(),
		})
		.nullish(),
});

export const getConnectorsResponse = z.array(searchSourceConnector);

/**
 * Get connector
 */
export const getConnectorRequest = searchSourceConnector.pick({ id: true });

export const getConnectorResponse = searchSourceConnector;

/**
 * Create connector
 */
export const createConnectorRequest = z.object({
	data: searchSourceConnector.pick({
		name: true,
		connector_type: true,
		is_indexable: true,
		is_active: true,
		last_indexed_at: true,
		config: true,
		enable_vision_llm: true,
		periodic_indexing_enabled: true,
		indexing_frequency_minutes: true,
		next_scheduled_at: true,
	}),
	queryParams: z.object({
		workspace_id: z.number().or(z.string()),
	}),
});

export const createConnectorResponse = searchSourceConnector;

/**
 * Update connector
 */
export const updateConnectorRequest = z.object({
	id: z.number(),
	/**
	 * The workspace whose settings change. Required by the server for a
	 * non-MCP connection's is_indexable / periodic-indexing fields: they live
	 * on that workspace's enablement row, not on the account-wide connection
	 * (custom/ACCOUNT_CONNECTIONS.md). Without it such a PUT answers 400.
	 */
	queryParams: z
		.object({
			workspace_id: z.number().or(z.string()),
		})
		.optional(),
	data: searchSourceConnector
		.pick({
			name: true,
			connector_type: true,
			is_indexable: true,
			is_active: true,
			last_indexed_at: true,
			config: true,
			enable_vision_llm: true,
			periodic_indexing_enabled: true,
			indexing_frequency_minutes: true,
			next_scheduled_at: true,
		})
		.partial(),
});

export const updateConnectorResponse = searchSourceConnector;

/**
 * Delete connector
 */
export const deleteConnectorRequest = searchSourceConnector.pick({ id: true });

export const deleteConnectorResponse = z.object({
	message: z.string(),
	status: z.string().optional(),
	connector_id: z.number().optional(),
	connector_name: z.string().optional(),
});

/**
 * Google Drive index request body
 */
export const googleDriveIndexBody = z.object({
	folders: z.array(
		z.object({
			id: z.string(),
			name: z.string(),
		})
	),
	files: z.array(
		z.object({
			id: z.string(),
			name: z.string(),
		})
	),
	indexing_options: z
		.object({
			max_files_per_folder: z.number().int().min(1).max(1000),
			incremental_sync: z.boolean(),
			include_subfolders: z.boolean(),
		})
		.optional(),
});

/**
 * Index connector
 */
export const indexConnectorRequest = z.object({
	connector_id: z.number(),
	queryParams: z.object({
		workspace_id: z.number().or(z.string()),
		start_date: z.string().optional(),
		end_date: z.string().optional(),
	}),
	body: googleDriveIndexBody.optional(),
});

export const indexConnectorResponse = z.object({
	message: z.string(),
	connector_id: z.number(),
	workspace_id: z.number(),
	indexing_from: z.string(),
	indexing_to: z.string(),
});

/**
 * Switch an account connection on/off in the current workspace.
 * Owner only — the backend 403s otherwise.
 */
export const setConnectorEnabledRequest = z.object({
	connectorId: z.number(),
	workspaceId: z.number(),
	enabled: z.boolean(),
});

export const setConnectorEnabledResponse = z.object({
	connector_id: z.number(),
	workspace_id: z.number(),
	enabled: z.boolean(),
	documents_removed: z.number().optional(),
});

// Inferred types
export type SearchSourceConnectorType = z.infer<typeof searchSourceConnectorTypeEnum>;
export type SearchSourceConnector = z.infer<typeof searchSourceConnector>;
export type GetConnectorsRequest = z.infer<typeof getConnectorsRequest>;
export type GetConnectorsResponse = z.infer<typeof getConnectorsResponse>;
export type GetConnectorRequest = z.infer<typeof getConnectorRequest>;
export type GetConnectorResponse = z.infer<typeof getConnectorResponse>;
export type CreateConnectorRequest = z.infer<typeof createConnectorRequest>;
export type CreateConnectorResponse = z.infer<typeof createConnectorResponse>;
export type UpdateConnectorRequest = z.infer<typeof updateConnectorRequest>;
export type UpdateConnectorResponse = z.infer<typeof updateConnectorResponse>;
export type DeleteConnectorRequest = z.infer<typeof deleteConnectorRequest>;
export type DeleteConnectorResponse = z.infer<typeof deleteConnectorResponse>;
export type IndexConnectorRequest = z.infer<typeof indexConnectorRequest>;
export type IndexConnectorResponse = z.infer<typeof indexConnectorResponse>;
export type SetConnectorEnabledRequest = z.infer<typeof setConnectorEnabledRequest>;
export type SetConnectorEnabledResponse = z.infer<typeof setConnectorEnabledResponse>;
