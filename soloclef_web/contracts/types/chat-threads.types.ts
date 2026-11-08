import { z } from "zod";

/**
 * Public chat snapshot info
 */
export const publicChatSnapshotInfo = z.object({
	id: z.number(),
	share_token: z.string(),
	public_url: z.string(),
	created_at: z.string(),
	message_count: z.number(),
});

/**
 * Create public chat snapshot
 */
export const publicChatSnapshotCreateRequest = z.object({
	thread_id: z.number(),
	/** 7 or 30 days; null never expires. Omitted means the server's default (7). */
	expires_in_days: z.union([z.literal(7), z.literal(30), z.null()]).optional(),
});

export const publicChatSnapshotCreateResponse = z.object({
	snapshot_id: z.number(),
	share_token: z.string(),
	public_url: z.string(),
	is_new: z.boolean(),
	expires_at: z.string().nullable().optional(),
});

/**
 * List public chat snapshots for thread
 */
export const publicChatSnapshotListRequest = z.object({
	thread_id: z.number(),
});

export const publicChatSnapshotListResponse = z.object({
	snapshots: z.array(publicChatSnapshotInfo),
});

/**
 * Delete public chat snapshot
 */
export const publicChatSnapshotDeleteRequest = z.object({
	thread_id: z.number(),
	snapshot_id: z.number(),
});

/**
 * Public chat snapshot with thread context
 */
export const publicChatSnapshotDetail = z.object({
	id: z.number(),
	share_token: z.string(),
	public_url: z.string(),
	created_at: z.string(),
	expires_at: z.string().nullable().optional(),
	is_expired: z.boolean().optional(),
	kind: z.enum(["chat", "output"]).optional(),
	message_count: z.number(),
	thread_id: z.number().nullable().optional(),
	thread_title: z.string(),
	notebook_id: z.number().nullable().optional(),
	created_by_user_id: z.string().nullable().optional(),
	created_by_name: z.string().nullable().optional(),
});

/**
 * List public chat snapshots for workspace
 */
export const publicChatSnapshotsBySpaceRequest = z.object({
	workspace_id: z.number(),
});

export const publicChatSnapshotsBySpaceResponse = z.object({
	snapshots: z.array(publicChatSnapshotDetail),
});

// Type exports
export type PublicChatSnapshotInfo = z.infer<typeof publicChatSnapshotInfo>;
export type PublicChatSnapshotCreateRequest = z.infer<typeof publicChatSnapshotCreateRequest>;
export type PublicChatSnapshotCreateResponse = z.infer<typeof publicChatSnapshotCreateResponse>;
export type PublicChatSnapshotListRequest = z.infer<typeof publicChatSnapshotListRequest>;
export type PublicChatSnapshotListResponse = z.infer<typeof publicChatSnapshotListResponse>;
export type PublicChatSnapshotDeleteRequest = z.infer<typeof publicChatSnapshotDeleteRequest>;
export type PublicChatSnapshotDetail = z.infer<typeof publicChatSnapshotDetail>;
export type PublicChatSnapshotsBySpaceRequest = z.infer<typeof publicChatSnapshotsBySpaceRequest>;
export type PublicChatSnapshotsBySpaceResponse = z.infer<typeof publicChatSnapshotsBySpaceResponse>;
