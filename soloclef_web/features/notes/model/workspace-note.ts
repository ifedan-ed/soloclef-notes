import { z } from "zod";
import { noteOriginSchema } from "@/features/notebooks/model/notebook";

const notebookRefSchema = z.object({
	id: z.number(),
	title: z.string(),
	icon: z.string().nullish(),
});
export type NotebookRef = z.infer<typeof notebookRefSchema>;

/** A folder of the documents tree: its names from the top, and its place in the tree. */
const folderRefSchema = z.object({
	id: z.number(),
	name: z.string(),
	path: z.array(z.string()),
	tree_order: z.number(),
});
export type FolderRef = z.infer<typeof folderRefSchema>;

/**
 * One row of the Notes page. ``kind`` says what ``id`` is: a notebook note
 * (it lives in ``notebook``) or a note document the assistant wrote as a file,
 * which no notebook shows until it is added to one.
 */
export const workspaceNoteSchema = z.object({
	kind: z.enum(["notebook_note", "document"]),
	id: z.number(),
	title: z.string(),
	preview: z.string(),
	origin: noteOriginSchema,
	notebook: notebookRefSchema.nullish(),
	source_in: z.array(notebookRefSchema).default([]),
	/** The folder a note document sits in; null at the top of the tree and for notebook notes. */
	folder: folderRefSchema.nullish(),
	/** ``id`` is null once the automation has been deleted. */
	automation: z.object({ id: z.number().nullable(), name: z.string() }).nullish(),
	created_at: z.string(),
	updated_at: z.string(),
});
export type WorkspaceNote = z.infer<typeof workspaceNoteSchema>;

/** A note document's text, from the document editor routes. */
export const noteDocumentContentSchema = z.object({
	document_id: z.number(),
	title: z.string(),
	source_markdown: z.string(),
});
