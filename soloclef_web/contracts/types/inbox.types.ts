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
	"site_announcement",
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
});

/**
 * An administrator's announcement: where it points and when it stops showing.
 */
export const siteAnnouncementMetadata = z.object({
	announcement_id: z.number(),
	link: z.string().nullable().optional(),
	expires_at: z.string().nullable().optional(),
});

/**
 * Union of all inbox item metadata types
 * Use this when the inbox item type is unknown
 */
export const inboxItemMetadata = z.union([
	connectorIndexingMetadata,
	connectorDeletionMetadata,
	documentProcessingMetadata,
	siteAnnouncementMetadata,
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
 * Notification category for tab-level filtering
 */
export const notificationCategory = z.enum(["status", "announcements"]);
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
 * Batched unread counts for every category in a single response, instead of
 * one /unread-count call per category.
 */
export const categoryUnreadCount = z.object({
	total_unread: z.number(),
	recent_unread: z.number(),
});

export const getBatchUnreadCountResponse = z.object({
	status: categoryUnreadCount,
	announcements: categoryUnreadCount.default({ total_unread: 0, recent_unread: 0 }),
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

export function isSiteAnnouncementMetadata(
	metadata: unknown
): metadata is SiteAnnouncementMetadata {
	return siteAnnouncementMetadata.safeParse(metadata).success;
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
export type SiteAnnouncementMetadata = z.infer<typeof siteAnnouncementMetadata>;
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
