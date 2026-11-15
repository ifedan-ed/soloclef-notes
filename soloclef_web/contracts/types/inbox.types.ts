import { z } from "zod";
import { searchSourceConnectorTypeEnum } from "./connector.types";
import { documentTypeEnum } from "./document.types";

/**
 * Inbox item type enum - matches backend notification types
 */
export const inboxItemTypeEnum = z.enum([
	"connector_indexing",
	"connector_deletion",
	"document_processing",
	"studio_output",
	"automation_run",
	"mail_draft",
	"app_update",
]);

/**
 * Inbox item status enum - used in metadata
 */
export const inboxItemStatusEnum = z.enum(["in_progress", "completed", "failed"]);

/**
 * Document processing stage enum
 */
export const documentProcessingStageEnum = z.enum([
	"queued",
	"parsing",
	"chunking",
	"embedding",
	"storing",
	"completed",
	"failed",
]);

/**
 * Base metadata schema shared across inbox item types
 */
export const baseInboxItemMetadata = z.object({
	operation_id: z.string().optional(),
	status: inboxItemStatusEnum.optional(),
	started_at: z.string().optional(),
	completed_at: z.string().optional(),
});

/**
 * Connector indexing metadata schema
 */
export const connectorIndexingMetadata = baseInboxItemMetadata.extend({
	connector_id: z.number(),
	connector_name: z.string(),
	connector_type: searchSourceConnectorTypeEnum,
	start_date: z.string().nullable().optional(),
	end_date: z.string().nullable().optional(),
	indexed_count: z.number(),
	total_count: z.number().optional(),
	progress_percent: z.number().optional(),
	error_message: z.string().nullable().optional(),
	// Google Drive specific fields
	folder_count: z.number().optional(),
	file_count: z.number().optional(),
	folder_names: z.array(z.string()).optional(),
	file_names: z.array(z.string()).optional(),
});

/**
 * Connector deletion metadata schema
 */
export const connectorDeletionMetadata = baseInboxItemMetadata.extend({
	connector_id: z.number(),
	connector_name: z.string(),
	connector_type: z.string(),
	documents_deleted: z.number(),
	error: z.string().optional(),
});

/**
 * Document processing metadata schema
 */
export const documentProcessingMetadata = baseInboxItemMetadata.extend({
	document_type: documentTypeEnum,
	document_name: z.string(),
	processing_stage: documentProcessingStageEnum,
	file_size: z.number().optional(),
	chunks_count: z.number().optional(),
	document_id: z.number().optional(),
	error_message: z.string().nullable().optional(),
	// A row can stand for several documents ("3 imports failed"): how many,
	// and up to five of their names. Its title already says the summary.
	count: z.number().optional(),
	names: z.array(z.string()).optional(),
	notebook_id: z.number().nullable().optional(),
});

/**
 * A Studio output finished or failed. A failure the person can simply run
 * again carries `retry: true` (the inbox's "Try again").
 */
export const studioOutputMetadata = baseInboxItemMetadata.extend({
	status: z.enum(["completed", "failed"]),
	notebook_id: z.number().nullable(),
	notebook_title: z.string().nullable(),
	output_id: z.number().nullable(),
	output_kind: z.enum([
		"slides",
		"report",
		"audio",
		"video",
		"quiz",
		"flashcards",
		"mindmap",
		"infographic",
		"data_table",
		"artifact",
	]),
	output_title: z.string(),
	retry: z.boolean(),
	error_message: z.string().nullable().optional(),
	thread_id: z.number().nullable().optional(),
});

/** An automation run ended: `status` is the inbox's two-way outcome, `run_status` the run's own. */
export const automationRunMetadata = baseInboxItemMetadata.extend({
	status: z.enum(["completed", "failed"]),
	run_status: z.enum(["succeeded", "failed", "needs_attention", "timed_out"]),
	automation_id: z.number(),
	automation_name: z.string(),
	run_id: z.number(),
});

/** A mail draft is ready to review; `link` is the page that shows it, when the mail feature gives one. */
export const mailDraftMetadata = baseInboxItemMetadata.extend({
	status: z.literal("completed"),
	draft_id: z.string(),
	link: z.string().nullable(),
	account_label: z.string().nullable().optional(),
});

/**
 * A newer iPhone app is in the App Store ("Soloclef 1.2.0 is out"), written
 * for an account with an iPhone on an older version. `store_url` is the
 * listing, which opening the item shows in a new tab.
 */
export const appUpdateMetadata = baseInboxItemMetadata.extend({
	status: z.literal("completed"),
	version: z.string(),
	store_url: z.string(),
});

/**
 * Union of all inbox item metadata types
 * Use this when the inbox item type is unknown
 */
export const inboxItemMetadata = z.union([
	connectorIndexingMetadata,
	connectorDeletionMetadata,
	documentProcessingMetadata,
	studioOutputMetadata,
	automationRunMetadata,
	mailDraftMetadata,
	appUpdateMetadata,
	baseInboxItemMetadata,
]);

/**
 * Main inbox item schema
 */
export const inboxItem = z.object({
	id: z.number(),
	user_id: z.string(),
	type: inboxItemTypeEnum,
	title: z.string(),
	message: z.string(),
	read: z.boolean(),
	metadata: z.record(z.string(), z.unknown()),
	created_at: z.string(),
	updated_at: z.string().nullable(),
});

/**
 * Typed inbox item schemas for specific types
 */
export const connectorIndexingInboxItem = inboxItem.extend({
	type: z.literal("connector_indexing"),
	metadata: connectorIndexingMetadata,
});

export const connectorDeletionInboxItem = inboxItem.extend({
	type: z.literal("connector_deletion"),
	metadata: connectorDeletionMetadata,
});

export const documentProcessingInboxItem = inboxItem.extend({
	type: z.literal("document_processing"),
	metadata: documentProcessingMetadata,
});

// =============================================================================
// API Request/Response Schemas
// =============================================================================

/**
 * The server's inbox category, which the list, unread-count and clear routes
 * take; status (uploads, syncs, Studio outputs, automation runs, mail drafts)
 * is the only one.
 */
export const notificationCategory = z.enum(["status"]);
export type NotificationCategory = z.infer<typeof notificationCategory>;

/**
 * Request schema for getting notifications
 */
export const getNotificationsRequest = z.object({
	queryParams: z.object({
		type: inboxItemTypeEnum.optional(),
		category: notificationCategory.optional(),
		source_type: z.string().optional(),
		filter: z.enum(["unread", "errors"]).optional(),
		before_date: z.string().optional(),
		search: z.string().optional(),
		limit: z.number().min(1).max(100).optional(),
		offset: z.number().min(0).optional(),
	}),
});

/**
 * Response schema for listing notifications
 */
export const getNotificationsResponse = z.object({
	items: z.array(inboxItem),
	total: z.number(),
	has_more: z.boolean(),
	next_offset: z.number().nullable(),
});

/**
 * Request schema for marking a single notification as read
 */
export const markNotificationReadRequest = z.object({
	notificationId: z.number(),
});

/**
 * Response schema for mark as read operations
 */
export const markNotificationReadResponse = z.object({
	success: z.boolean(),
	message: z.string(),
});

/**
 * Response schema for mark all as read operation
 */
export const markAllNotificationsReadResponse = z.object({
	success: z.boolean(),
	message: z.string(),
	updated_count: z.number(),
});

/**
 * Request schema for deleting one notification
 */
export const deleteNotificationRequest = z.object({
	notificationId: z.number(),
});

export const deleteNotificationResponse = z.object({
	success: z.boolean(),
	message: z.string(),
});

/**
 * Request schema for clearing an inbox view: the list's own filters, so it
 * deletes exactly what that view lists.
 */
export const clearNotificationsRequest = z.object({
	queryParams: z.object({
		category: notificationCategory.optional(),
		filter: z.enum(["unread", "errors"]).optional(),
	}),
});

export const clearNotificationsResponse = z.object({
	success: z.boolean(),
	message: z.string(),
	deleted_count: z.number(),
});

/**
 * Response schema for unread count
 * Returns both total and recent counts for split tracking
 */
export const getUnreadCountResponse = z.object({
	total_unread: z.number(),
	recent_unread: z.number(), // Within SYNC_WINDOW_DAYS (14 days)
});

/**
 * Response schema for notification source types (status tab filter)
 */
export const sourceTypeItem = z.object({
	key: z.string(),
	type: z.string(),
	category: z.enum(["connector", "document"]),
	count: z.number(),
});

export const getSourceTypesResponse = z.object({
	sources: z.array(sourceTypeItem),
});

/**
 * Unread counts keyed by category, from /unread-counts-batch; status is the
 * only category.
 */
export const categoryUnreadCount = z.object({
	total_unread: z.number(),
	recent_unread: z.number(),
});

export const getBatchUnreadCountResponse = z.object({
	status: categoryUnreadCount,
});

// =============================================================================
// Type Guards for Metadata
// =============================================================================

/**
 * Type guard for ConnectorIndexingMetadata
 */
export function isConnectorIndexingMetadata(
	metadata: unknown
): metadata is ConnectorIndexingMetadata {
	return connectorIndexingMetadata.safeParse(metadata).success;
}

/**
 * Type guard for ConnectorDeletionMetadata
 */
export function isConnectorDeletionMetadata(
	metadata: unknown
): metadata is ConnectorDeletionMetadata {
	return connectorDeletionMetadata.safeParse(metadata).success;
}

/**
 * Type guard for DocumentProcessingMetadata
 */
export function isDocumentProcessingMetadata(
	metadata: unknown
): metadata is DocumentProcessingMetadata {
	return documentProcessingMetadata.safeParse(metadata).success;
}

/**
 * Safe metadata parser - returns typed metadata or null
 */
export function parseInboxItemMetadata(
	type: InboxItemTypeEnum,
	metadata: unknown
): ConnectorIndexingMetadata | ConnectorDeletionMetadata | DocumentProcessingMetadata | null {
	switch (type) {
		case "connector_indexing": {
			const result = connectorIndexingMetadata.safeParse(metadata);
			return result.success ? result.data : null;
		}
		case "connector_deletion": {
			const result = connectorDeletionMetadata.safeParse(metadata);
			return result.success ? result.data : null;
		}
		case "document_processing": {
			const result = documentProcessingMetadata.safeParse(metadata);
			return result.success ? result.data : null;
		}
		default:
			return null;
	}
}

// =============================================================================
// Inferred types
// =============================================================================

export type InboxItemTypeEnum = z.infer<typeof inboxItemTypeEnum>;
export type InboxItemStatusEnum = z.infer<typeof inboxItemStatusEnum>;
export type DocumentProcessingStageEnum = z.infer<typeof documentProcessingStageEnum>;
export type BaseInboxItemMetadata = z.infer<typeof baseInboxItemMetadata>;
export type ConnectorIndexingMetadata = z.infer<typeof connectorIndexingMetadata>;
export type ConnectorDeletionMetadata = z.infer<typeof connectorDeletionMetadata>;
export type DocumentProcessingMetadata = z.infer<typeof documentProcessingMetadata>;
export type StudioOutputMetadata = z.infer<typeof studioOutputMetadata>;
export type AutomationRunMetadata = z.infer<typeof automationRunMetadata>;
export type MailDraftMetadata = z.infer<typeof mailDraftMetadata>;
export type InboxItemMetadata = z.infer<typeof inboxItemMetadata>;
export type InboxItem = z.infer<typeof inboxItem>;
export type ConnectorIndexingInboxItem = z.infer<typeof connectorIndexingInboxItem>;
export type ConnectorDeletionInboxItem = z.infer<typeof connectorDeletionInboxItem>;
export type DocumentProcessingInboxItem = z.infer<typeof documentProcessingInboxItem>;

// API Request/Response types
export type GetNotificationsRequest = z.infer<typeof getNotificationsRequest>;
export type GetNotificationsResponse = z.infer<typeof getNotificationsResponse>;
export type MarkNotificationReadRequest = z.infer<typeof markNotificationReadRequest>;
export type MarkNotificationReadResponse = z.infer<typeof markNotificationReadResponse>;
export type MarkAllNotificationsReadResponse = z.infer<typeof markAllNotificationsReadResponse>;
export type DeleteNotificationRequest = z.infer<typeof deleteNotificationRequest>;
export type DeleteNotificationResponse = z.infer<typeof deleteNotificationResponse>;
export type ClearNotificationsRequest = z.infer<typeof clearNotificationsRequest>;
export type ClearNotificationsResponse = z.infer<typeof clearNotificationsResponse>;
export type GetUnreadCountResponse = z.infer<typeof getUnreadCountResponse>;
export type SourceTypeItem = z.infer<typeof sourceTypeItem>;
export type GetSourceTypesResponse = z.infer<typeof getSourceTypesResponse>;
export type GetBatchUnreadCountResponse = z.infer<typeof getBatchUnreadCountResponse>;
