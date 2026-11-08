import { z } from "zod";

export const notebookSchema = z.object({
	id: z.number(),
	workspace_id: z.number(),
	title: z.string(),
	icon: z.string().nullable(),
	created_at: z.string(),
	updated_at: z.string(),
	source_count: z.number(),
	enabled_source_count: z.number(),
	chat_goal: z.enum(["default", "learning_guide", "custom"]).default("default"),
	chat_instructions: z.string().nullish(),
	response_length: z.enum(["default", "longer", "shorter"]).default("default"),
	featured: z.boolean().default(false),
	/** "auto" while the notebook may still name itself; "user" once renamed. */
	title_source: z.enum(["auto", "user"]).default("user"),
	/** A source is indexing or just finished: the automatic name is seconds away. */
	title_pending: z.boolean().default(false),
});
export type Notebook = z.infer<typeof notebookSchema>;
export type ChatGoal = Notebook["chat_goal"];
export type ResponseLength = Notebook["response_length"];

export const studioSuggestionsSchema = z.object({
	suggestions: z.array(z.string()),
	placeholder: z.string().nullish(),
	/** Every idea generated for this dialog; the dialog shows three at a time. */
	pool: z.array(z.string()).default([]),
	examples: z.array(z.string()).default([]),
});
export type StudioSuggestions = z.infer<typeof studioSuggestionsSchema>;

export const notebookSourceSchema = z.object({
	id: z.number(),
	kind: z.enum(["document", "folder"]),
	target_id: z.number(),
	title: z.string(),
	document_type: z.string().nullish(),
	status: z.string().nullish(),
	enabled: z.boolean(),
	added_at: z.string(),
});
export type NotebookSource = z.infer<typeof notebookSourceSchema>;

export const noteOriginSchema = z.enum(["hand", "chat", "automation"]);
export type NoteOrigin = z.infer<typeof noteOriginSchema>;

export const notebookNoteSchema = z.object({
	id: z.number(),
	notebook_id: z.number(),
	title: z.string(),
	content: z.string(),
	converted_document_id: z.number().nullable(),
	/** Add note ("hand"), Save to note ("chat"), or an automation's result. */
	origin: noteOriginSchema.default("hand"),
	automation_id: z.number().nullish(),
	created_at: z.string(),
	updated_at: z.string(),
});
export type NotebookNote = z.infer<typeof notebookNoteSchema>;

export const notebookThreadSchema = z.object({
	id: z.number(),
	title: z.string(),
	created_at: z.string(),
	updated_at: z.string(),
});
export type NotebookThread = z.infer<typeof notebookThreadSchema>;

export const sourceGuideSchema = z.object({
	document_id: z.number(),
	title: z.string(),
	summary: z.string().nullable(),
	status: z.enum(["ready", "unavailable"]),
});
export type SourceGuide = z.infer<typeof sourceGuideSchema>;

export const studioKinds = [
	"audio",
	"video",
	"report",
	"slides",
	"mindmap",
	"flashcards",
	"quiz",
	"data_table",
	"infographic",
] as const;
export type StudioKind = (typeof studioKinds)[number];

export const studioOutputSchema = z.object({
	id: z.number(),
	kind: z.enum(studioKinds),
	title: z.string(),
	status: z.enum(["queued", "running", "ready", "failed", "cancelled"]),
	phase: z.string().nullish(),
	progress: z.number().nullish(),
	failure_code: z.string().nullish(),
	artifact_id: z.number().nullish(),
	artifact_format: z.string().nullish(),
	duration_seconds: z.number().nullish(),
	source_count: z.number(),
	options: z.record(z.string(), z.unknown()),
	/** Which model wrote it; ``fallback`` when the primary model failed. */
	generated_by: z
		.object({ model: z.string().nullish(), fallback: z.boolean().nullish() })
		.nullish(),
	created_at: z.string(),
});
export type StudioOutput = z.infer<typeof studioOutputSchema>;

export const reportTemplateSchema = z.object({
	key: z.string(),
	format: z.enum(["interactive", "document"]),
	title: z.string(),
	description: z.string(),
	prompt: z.string(),
	customized: z.boolean(),
});
export type ReportTemplate = z.infer<typeof reportTemplateSchema>;

/** A Studio output proposed under one section of a report (the reading view). */
export const reportSuggestionSchema = z.object({
	id: z.string(),
	section_index: z.number(),
	section_heading: z.string(),
	kind: z.enum(studioKinds),
	title: z.string(),
	focus: z.string(),
	state: z.enum(["open", "added", "dismissed"]),
	output_id: z.number().nullish(),
});
export type ReportSuggestion = z.infer<typeof reportSuggestionSchema>;

export const reportViewSchema = z.object({
	output_id: z.number(),
	artifact_id: z.number(),
	generation: z.number(),
	format: z.enum(["document", "interactive"]),
	language: z.string(),
	suggestions_status: z.enum(["ready", "unavailable"]),
	suggestions: z.array(reportSuggestionSchema),
	sources: z.array(
		z.object({ id: z.number(), title: z.string(), document_type: z.string().nullish() })
	),
});
export type ReportView = z.infer<typeof reportViewSchema>;

/** What a Studio output's viewer shows around it: the prompt and sources. */
export const outputViewSchema = z.object({
	output_id: z.number(),
	artifact_id: z.number(),
	kind: z.string(),
	prompt: z.string(),
	sources: z.array(
		z.object({ id: z.number(), title: z.string(), document_type: z.string().nullish() })
	),
});
export type OutputView = z.infer<typeof outputViewSchema>;

/** One passage the home search found inside a notebook: a source's or a note's. */
export const searchPassageSchema = z.object({
	kind: z.enum(["source", "note"]),
	title: z.string(),
	snippet: z.string(),
	/** [start, end) ranges of ``snippet`` to highlight. */
	highlights: z.array(z.tuple([z.number(), z.number()])).default([]),
	document_id: z.number().nullish(),
	note_id: z.number().nullish(),
	/** The notebook source row (a folder's row for a document inside a source folder). */
	source_id: z.number().nullish(),
	folder_name: z.string().nullish(),
	document_type: z.string().nullish(),
	score: z.number(),
});
export type SearchPassage = z.infer<typeof searchPassageSchema>;

export const notebookSearchSchema = z.object({
	query: z.string(),
	notebooks: z.array(
		z.object({
			id: z.number(),
			title: z.string(),
			icon: z.string().nullish(),
			updated_at: z.string(),
			passages: z.array(searchPassageSchema),
		})
	),
	took_ms: z.number(),
});
export type NotebookSearch = z.infer<typeof notebookSearchSchema>;
export type NotebookSearchHit = NotebookSearch["notebooks"][number];
