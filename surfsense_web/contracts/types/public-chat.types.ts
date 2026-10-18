import { z } from "zod";

/**
 * Author info for public chat
 */
export const publicAuthor = z.object({
	display_name: z.string().nullable(),
	avatar_url: z.string().nullable(),
});

/**
 * Message in a public chat
 */
export const publicChatMessage = z.object({
	role: z.string(),
	content: z.unknown(),
	author: publicAuthor.nullable(),
	created_at: z.string(),
});

/**
 * Thread info for public chat
 */
export const publicChatThread = z.object({
	title: z.string(),
	created_at: z.string(),
});

/**
 * Get public chat
 */
export const getPublicChatRequest = z.object({
	share_token: z.string(),
});

/**
 * A shared Studio output's main file, as servers before ``files`` described it.
 */
export const publicSharedFile = z.object({
	filename: z.string(),
	mime_type: z.string(),
	size_bytes: z.number(),
	display: z.enum(["image", "pdf", "audio", "video", "file"]),
});

/** One file of a shared output, in the in-app manifest's shape, at the link's own address. */
export const publicSharedArtifactFile = z.object({
	file_id: z.number(),
	role: z.enum(["primary", "preview"]),
	filename: z.string(),
	mime_type: z.string(),
	size_bytes: z.number(),
	content_url: z.string(),
});

/**
 * A shared Studio output: its text as shared and its artifact's files, which
 * the public page opens in the owner's viewer for its kind.
 */
export const publicSharedOutput = z.object({
	title: z.string(),
	kind: z.string(),
	format: z.string(),
	markdown: z.string(),
	artifact_id: z.number(),
	file: publicSharedFile.nullable().optional(),
	generation: z.number().int().positive().optional(),
	files: z.array(publicSharedArtifactFile).optional(),
});

export const getPublicChatResponse = z.object({
	kind: z.enum(["chat", "output"]).default("chat"),
	thread: publicChatThread,
	messages: z.array(publicChatMessage),
	output: publicSharedOutput.nullable().optional(),
	expires_at: z.string().nullable().optional(),
});

/**
 * Clone public chat
 */
export const clonePublicChatRequest = z.object({
	share_token: z.string(),
});

export const clonePublicChatResponse = z.object({
	thread_id: z.number(),
	workspace_id: z.number(),
	notebook_id: z.number().nullable().optional(),
});

// Type exports
export type PublicAuthor = z.infer<typeof publicAuthor>;
export type PublicChatMessage = z.infer<typeof publicChatMessage>;
export type PublicChatThread = z.infer<typeof publicChatThread>;
export type GetPublicChatRequest = z.infer<typeof getPublicChatRequest>;
export type PublicSharedFile = z.infer<typeof publicSharedFile>;
export type PublicSharedArtifactFile = z.infer<typeof publicSharedArtifactFile>;
export type PublicSharedOutput = z.infer<typeof publicSharedOutput>;
export type GetPublicChatResponse = z.infer<typeof getPublicChatResponse>;
export type ClonePublicChatRequest = z.infer<typeof clonePublicChatRequest>;
export type ClonePublicChatResponse = z.infer<typeof clonePublicChatResponse>;
