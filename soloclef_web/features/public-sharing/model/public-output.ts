import { z } from "zod";

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

/**
 * What GET /api/v1/public/{token} answers. Only a Studio output can be
 * shared, so `kind` is always "output" and anything else fails to parse (the
 * page then says it could not load). The server still sends `thread` and an
 * empty `messages` for phone apps installed before chat links were removed;
 * this page reads neither, so neither is in the schema.
 */
export const publicOutputPage = z.object({
	kind: z.literal("output"),
	output: publicSharedOutput,
	expires_at: z.string().nullable().optional(),
});

export type PublicSharedFile = z.infer<typeof publicSharedFile>;
export type PublicSharedArtifactFile = z.infer<typeof publicSharedArtifactFile>;
export type PublicSharedOutput = z.infer<typeof publicSharedOutput>;
export type PublicOutputPage = z.infer<typeof publicOutputPage>;
