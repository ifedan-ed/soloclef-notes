import { z } from "zod";
import { paginationQueryParams } from ".";
import { documentParserEnum } from "./document.types";

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
	// Whether the connection is switched on for its owner's chats and
	// indexing. It is the connection's own field, changed with
	// PUT .../{id}/enabled.
	enabled: z.boolean(),
	is_indexable: z.boolean(),
	is_active: z.boolean().default(true),
	last_indexed_at: z.string().nullable(),
	config: z.record(z.string(), z.any()),
	enable_vision_llm: z.boolean().default(false),
	periodic_indexing_enabled: z.boolean(),
	indexing_frequency_minutes: z.number().nullable(),
	next_scheduled_at: z.string().nullable(),
	// Always the signed-in account: the server lists and answers only the
	// caller's own connections (someone else's id is a 404).
	user_id: z.string(),
	created_at: z.string(),
});

/**
 * Get connectors
 */
export const getConnectorsRequest = z.object({
	queryParams: paginationQueryParams.pick({ skip: true, limit: true }).nullish(),
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
});

export const createConnectorResponse = searchSourceConnector;

/**
 * Update connector
 */
export const updateConnectorRequest = z.object({
	id: z.number(),
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
		// An update changes only the fields it names. The read schema's
		// defaults would otherwise fill in the two it leaves out, and every
		// rename would have switched the vision model off.
		.extend({ is_active: z.boolean(), enable_vision_llm: z.boolean() })
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
	// The date range to index; absent for a connector that has none (web
	// pages, a Google Drive folder selection).
	queryParams: z
		.object({
			start_date: z.string().optional(),
			end_date: z.string().optional(),
		})
		.optional(),
	body: googleDriveIndexBody.optional(),
});

// The sync is queued, not finished: the answer says so and names the
// connection. It reports no date range, because a Drive sync reads the
// change feed and never read one.
export const indexConnectorResponse = z.object({
	message: z.string(),
	connector_id: z.number(),
});

/**
 * Switch a connection on or off (PUT .../{id}/enabled). The server answers
 * the connection as it now stands, so the caller needs no second read.
 */
export const setConnectorEnabledRequest = z.object({
	connectorId: z.number(),
	enabled: z.boolean(),
});

export const setConnectorEnabledResponse = searchSourceConnector;

/**
 * The parser that reads the files a connection brings in. It has a route of
 * its own (GET and PUT on .../{id}/parser) and is not a field of the
 * connection read: only the account that owns the connection may see or
 * change it, and a save is validated against what this server has
 * configured, so a 400 names the parser that is not.
 *
 * `parser` is null on a connection nobody has chosen for; the server's
 * default parser reads its files until someone does.
 */
export const connectorParser = z.object({
	id: z.number(),
	parser: documentParserEnum.nullable(),
});

export const setConnectorParserRequest = z.object({
	connectorId: z.number(),
	parser: documentParserEnum,
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
export type SetConnectorParserRequest = z.infer<typeof setConnectorParserRequest>;
export type ConnectorParser = z.infer<typeof connectorParser>;
