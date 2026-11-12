import { z } from "zod";

/** A notebook as a note names it: where it is filed, or what reads it. */
export const noteNotebookSchema = z.object({
	id: z.number(),
	title: z.string(),
	icon: z.string().nullable(),
});
export type NoteNotebook = z.infer<typeof noteNotebookSchema>;

/** Written by hand, kept from a chat answer, or written by an automation. */
export const noteOriginSchema = z.enum(["hand", "chat", "automation"]);
export type NoteOrigin = z.infer<typeof noteOriginSchema>;

/**
 * Whether the assistant can read the note's newest text: "pending" and
 * "processing" mean it is still catching up after a save.
 */
export const noteIndexStateSchema = z.enum(["ready", "pending", "processing", "failed"]);
export type NoteIndexState = z.infer<typeof noteIndexStateSchema>;

const noteFields = {
	id: z.number(),
	title: z.string(),
	origin: noteOriginSchema,
	/** The notebook it is filed in; null for a note in none. */
	notebook: noteNotebookSchema.nullable(),
	automation_id: z.number().nullable(),
	/** The conversation and answer a kept note came from. */
	thread_id: z.number().nullable(),
	message_id: z.number().nullable(),
	pinned: z.boolean(),
	pinned_at: z.string().nullable(),
	/** The notebooks whose chat reads this note (the source switch). */
	sources: z.array(noteNotebookSchema),
	index_state: noteIndexStateSchema.catch("ready"),
	created_at: z.string(),
	updated_at: z.string(),
};

/** A note as a list shows it: the start of its text, or the passage a search matched. */
export const noteSummarySchema = z.object({ ...noteFields, preview: z.string() });
export type NoteSummary = z.infer<typeof noteSummarySchema>;

/** A note with its whole text, as it opens. */
export const noteSchema = z.object({
	...noteFields,
	/** Markdown: what the editor reads and writes, and what the assistant indexes. */
	content: z.string(),
	/** The notebook whose chat a kept answer came from: where "From a chat" opens. */
	chat_notebook_id: z.number().nullable(),
});
export type Note = z.infer<typeof noteSchema>;

export const notesPageSchema = z.object({
	items: z.array(noteSummarySchema),
	total: z.number(),
	offset: z.number(),
	limit: z.number(),
	has_more: z.boolean(),
});
export type NotesPage = z.infer<typeof notesPageSchema>;
