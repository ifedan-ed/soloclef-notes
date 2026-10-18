/**
 * Wire contracts, mirrored from the web app so both clients read the backend
 * the same way. Sources:
 *   surfsense_web/features/notebooks/model/notebook.ts
 *   surfsense_web/features/artifacts/model/artifact.ts
 *   surfsense_web/contracts/types/{workspace,user,document,podcast}.types.ts
 *   surfsense_backend/app/schemas/auth.py (RefreshTokenResponse)
 * Keep field names and defaults identical when either side changes.
 */
import { z } from "zod";

export const tokenResponseSchema = z.object({
	access_token: z.string(),
	refresh_token: z.string().nullish(),
	token_type: z.string().default("bearer"),
	access_expires_at: z.number(),
});

export const userSchema = z.object({
	id: z.string(),
	email: z.string(),
	is_active: z.boolean(),
	is_superuser: z.boolean().optional(),
	is_verified: z.boolean().optional(),
	display_name: z.string().nullish(),
	avatar_url: z.string().nullish(),
});
export type User = z.infer<typeof userSchema>;

export const workspaceSchema = z.object({
	id: z.number(),
	name: z.string(),
	description: z.string().nullable().optional(),
	created_at: z.string(),
	is_owner: z.boolean().optional(),
	member_count: z.number().optional(),
});
export type Workspace = z.infer<typeof workspaceSchema>;

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
});
export type Notebook = z.infer<typeof notebookSchema>;

export const studioSuggestionsSchema = z.object({
	suggestions: z.array(z.string()),
	placeholder: z.string().nullish(),
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

/** The home search's hits inside notebooks (web: model/notebook.ts). */
export const searchPassageSchema = z.object({
	kind: z.enum(["source", "note"]),
	title: z.string(),
	snippet: z.string(),
	highlights: z.array(z.tuple([z.number(), z.number()])).default([]),
	document_id: z.number().nullish(),
	note_id: z.number().nullish(),
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
	generated_by: z.object({ model: z.string().nullish(), fallback: z.boolean().nullish() }).nullish(),
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

export const followupsSchema = z.object({ suggestions: z.array(z.string()) });

export const artifactFileSchema = z.object({
	file_id: z.number(),
	role: z.enum(["primary", "preview"]),
	filename: z.string(),
	mime_type: z.string(),
	size_bytes: z.number(),
	content_url: z.string(),
});
export type ArtifactFile = z.infer<typeof artifactFileSchema>;

export const artifactManifestSchema = z.object({
	artifact_id: z.number(),
	document_id: z.number(),
	title: z.string(),
	format: z.string(),
	generation: z.number().int().positive(),
	markdown_representation: z.string(),
	files: z.array(artifactFileSchema),
	updated_at: z.string().nullable(),
});
export type ArtifactManifest = z.infer<typeof artifactManifestSchema>;

export const documentParserSchema = z.enum(["local", "cloud"]);
export type DocumentParser = z.infer<typeof documentParserSchema>;

export const etlCapabilitiesSchema = z.object({
	default_parser: documentParserSchema,
	parsers: z.array(
		z.object({
			parser: documentParserSchema,
			service: z.string().nullable(),
			available: z.boolean(),
			unavailable_reason: z.string().nullable(),
			page_multiplier: z.number().int().min(0),
			document_extensions: z.array(z.string()),
		})
	),
	always_handled: z.object({
		plaintext: z.array(z.string()),
		direct_convert: z.array(z.string()),
		audio: z.array(z.string()),
		image: z.array(z.string()),
	}),
	page_credit_billing_enabled: z.boolean(),
});
export type EtlCapabilities = z.infer<typeof etlCapabilitiesSchema>;

export const uploadResponseSchema = z.object({
	message: z.string(),
	document_ids: z.array(z.number()),
	duplicate_document_ids: z.array(z.number()).optional(),
	total_files: z.number().optional(),
	pending_files: z.number().optional(),
	skipped_duplicates: z.number().optional(),
});
export type UploadResponse = z.infer<typeof uploadResponseSchema>;

export const languageOptionsSchema = z.object({
	languages: z.array(z.string()),
	allows_custom: z.boolean(),
});

/** One persisted chat message; ``content`` is the same part list the stream builds. */
export const messageRecordSchema = z.object({
	id: z.number(),
	thread_id: z.number(),
	role: z.enum(["user", "assistant", "system"]),
	content: z.unknown(),
	created_at: z.string(),
});
export type MessageRecord = z.infer<typeof messageRecordSchema>;

export const threadHistorySchema = z.object({ messages: z.array(messageRecordSchema) });

export const documentByChunkSchema = z.object({
	id: z.number(),
	title: z.string(),
	document_type: z.string(),
	document_metadata: z.record(z.string(), z.unknown()),
	chunks: z.array(z.object({ id: z.number(), content: z.string() })),
});
export type DocumentByChunk = z.infer<typeof documentByChunkSchema>;

/**
 * ``GET /api/v1/billing/me`` (app/billing/routes/user.py). The route has no
 * response model, so only the fields the Settings screen reads are declared;
 * the rest (catalog, payments, account dates) passes through untouched.
 */
export const billingMeSchema = z.looseObject({
	mode: z.string(),
	exempt: z.boolean(),
	plan: z.looseObject({ id: z.number(), slug: z.string(), name: z.string() }).nullable(),
	balances: z.looseObject({
		plan_credits: z.number(),
		plan_allowance: z.number(),
		topup_credits: z.number(),
	}),
});
export type BillingMe = z.infer<typeof billingMeSchema>;
