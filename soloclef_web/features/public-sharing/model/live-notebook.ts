import { z } from "zod";

/**
 * What a visitor to a live notebook link is sent: the notebook as it is now.
 * Ids here are the ones the public routes take back, each re-checked there.
 */
export const liveCitationSchema = z.object({
	source_id: z.number(),
	title: z.string(),
	page: z.number().nullable(),
});
export type LiveCitation = z.infer<typeof liveCitationSchema>;

export const liveMessageSchema = z.object({
	role: z.enum(["user", "assistant"]),
	content: z.array(z.record(z.string(), z.unknown())),
	created_at: z.string().nullable(),
});
export type LiveMessage = z.infer<typeof liveMessageSchema>;

export const liveOutputSchema = z.object({
	id: z.number(),
	kind: z.string(),
	title: z.string(),
	format: z.string().nullable(),
	artifact_id: z.number(),
	duration_seconds: z.number().nullable(),
	created_at: z.string(),
});
export type LiveOutput = z.infer<typeof liveOutputSchema>;

export const liveSourceSchema = z.object({
	id: z.number(),
	title: z.string(),
	document_type: z.string(),
	has_file: z.boolean(),
});
export type LiveSource = z.infer<typeof liveSourceSchema>;

export const liveNotebookSchema = z.object({
	notebook: z.object({ title: z.string(), icon: z.string().nullable() }),
	expires_at: z.string().nullable(),
	messages: z.array(liveMessageSchema),
	citations: z.record(z.string(), liveCitationSchema),
	outputs: z.array(liveOutputSchema),
	sources: z.array(liveSourceSchema),
});
export type LiveNotebook = z.infer<typeof liveNotebookSchema>;

export const liveSourceViewSchema = z.object({
	id: z.number(),
	title: z.string(),
	document_type: z.string(),
	presentation: z.enum(["original", "text"]),
	file: z
		.object({
			file_id: z.number(),
			filename: z.string(),
			mime_type: z.string(),
			size_bytes: z.number(),
			content_url: z.string(),
		})
		.nullable(),
	markdown: z.string(),
});
export type LiveSourceView = z.infer<typeof liveSourceViewSchema>;
