/**
 * @jest-environment node
 *
 * The app's zod schemas, routes and request bodies checked against the
 * backend's own OpenAPI document (snapshot in openapi.snapshot.json, refreshed
 * with scripts/fetch-openapi.mjs). A field the backend renames or drops, a
 * route that moves, or an option value it stops accepting fails here.
 */
import type { ZodObject, ZodType } from "zod";
import snapshot from "./openapi.snapshot.json";
import { ROUTES_USED } from "./routes-used";
import { buildChatRequest } from "../../chat/request";
import { STUDIO_TILES } from "../../studio/catalog";
import { buildStudioRequest, initialDialogState } from "../../studio/request";
import {
	billingMeSchema,
	documentByChunkSchema,
	etlCapabilitiesSchema,
	followupsSchema,
	languageOptionsSchema,
	messageRecordSchema,
	notebookSchema,
	notebookSearchSchema,
	notebookSourceSchema,
	searchPassageSchema,
	notebookThreadSchema,
	reportTemplateSchema,
	sourceGuideSchema,
	studioOutputSchema,
	studioSuggestionsSchema,
	tokenResponseSchema,
	userSchema,
	workspaceSchema,
} from "../schemas";

type JsonSchema = {
	properties?: Record<string, JsonSchema>;
	required?: string[];
	default?: unknown;
	enum?: unknown[];
	const?: unknown;
	anyOf?: JsonSchema[];
	$ref?: string;
	items?: JsonSchema;
};

const components = snapshot.components.schemas as Record<string, JsonSchema>;

function component(name: string): JsonSchema {
	const schema = components[name];
	if (!schema) throw new Error(`no component ${name} in the snapshot`);
	return schema;
}

/** Keys a zod object requires the backend to send (no default, not optional). */
function requiredKeys(schema: ZodType): string[] {
	const shape = (schema as unknown as ZodObject).shape as Record<string, ZodType>;
	return Object.entries(shape)
		.filter(([, field]) => !field.safeParse(undefined).success)
		.map(([key]) => key);
}

function allKeys(schema: ZodType): string[] {
	return Object.keys((schema as unknown as ZodObject).shape);
}

/** Every key the app reads exists; every key it insists on is always sent. */
function expectMatches(schema: ZodType, name: string) {
	const backend = component(name);
	const properties = backend.properties ?? {};
	for (const key of allKeys(schema)) {
		expect({ component: name, key, present: key in properties }).toEqual({ component: name, key, present: true });
	}
	for (const key of requiredKeys(schema)) {
		const always = (backend.required ?? []).includes(key) || "default" in (properties[key] ?? {});
		expect({ component: name, key, alwaysSent: always }).toEqual({ component: name, key, alwaysSent: true });
	}
}

describe("response schemas match the backend", () => {
	it.each([
		[tokenResponseSchema, "RefreshTokenResponse"],
		[userSchema, "UserRead"],
		[workspaceSchema, "WorkspaceWithStats"],
		[notebookSchema, "NotebookRead"],
		[notebookSourceSchema, "SourceRead"],
		[notebookSearchSchema, "NotebookSearchRead"],
		[searchPassageSchema, "SearchPassage"],
		[sourceGuideSchema, "SourceGuideRead"],
		[notebookThreadSchema, "NotebookThreadRead"],
		[studioOutputSchema, "StudioOutputRead"],
		[studioSuggestionsSchema, "SuggestionsRead"],
		[reportTemplateSchema, "ReportTemplateRead"],
		[followupsSchema, "FollowupsRead"],
		[messageRecordSchema, "NewChatMessageRead"],
		[documentByChunkSchema, "DocumentWithChunksRead"],
		[etlCapabilitiesSchema, "EtlCapabilitiesRead"],
		[languageOptionsSchema, "LanguageOptions"],
	] as const)("%#: %s", (schema, name) => {
		expectMatches(schema as ZodType, name);
	});

	it("knows every studio kind and status the backend reports", () => {
		const output = component("StudioOutputRead").properties ?? {};
		const kinds = new Set(STUDIO_TILES.map((tile) => tile.kind));
		const backendKinds = output.kind.enum ?? components[output.kind.$ref?.split("/").pop() ?? ""]?.enum ?? [];
		for (const kind of backendKinds) expect(kinds.has(kind as never)).toBe(true);
	});
});

/**
 * ``GET /api/v1/billing/me`` declares no response model (its OpenAPI schema is
 * empty), so the app's schema is checked against a body shaped exactly like
 * the dict app/billing/routes/user.py:my_billing returns.
 */
describe("billing/me", () => {
	const plan = {
		id: 1,
		slug: "free",
		name: "Free",
		tagline: "",
		features: [],
		price_cents: 0,
		currency: "usd",
		monthly_credits: 100,
		entitlements: {},
		is_default: true,
		public: true,
		active: true,
		sort_order: 0,
	};
	const body = {
		mode: "shadow",
		is_admin: false,
		exempt: false,
		plan,
		account: {
			plan_id: 1,
			plan_source: "default",
			plan_status: "active",
			period_start: "2026-09-01T00:00:00Z",
			period_end: "2026-10-01T00:00:00Z",
			paid_until: null,
			cancel_at_period_end: false,
			has_subscription: false,
			exempt: false,
		},
		balances: {
			plan_credits: 42.5,
			plan_allowance: 100,
			topup_credits: 0,
			reserved_credits: 0,
			spendable_credits: 42.5,
			period_used_credits: 57.5,
		},
		low_balance_credits: 20,
		payments: { card: false, crypto: { enabled: false, currencies: [], month_options: [1], discounts: {} } },
		plans: [plan],
		prices: [],
		packs: [],
	};

	it("parses the route's response and keeps the fields it does not read", () => {
		const parsed = billingMeSchema.parse(body);
		expect(parsed.balances.plan_credits).toBe(42.5);
		expect(parsed.plan?.name).toBe("Free");
		expect((parsed as Record<string, unknown>).payments).toEqual(body.payments);
	});

	it("accepts an account with no plan row", () => {
		expect(billingMeSchema.safeParse({ ...body, plan: null }).success).toBe(true);
	});

	it("is in the snapshot", () => {
		const entry = (snapshot.paths as Record<string, Record<string, unknown>>)["/api/v1/billing/me"];
		expect(entry?.get).toBeTruthy();
	});
});

describe("routes exist with the methods the app calls", () => {
	it.each(ROUTES_USED)("%s %s", (method, path) => {
		const entry = (snapshot.paths as Record<string, Record<string, unknown>>)[path];
		expect(entry?.[method.toLowerCase()]).toBeTruthy();
	});
});

describe("request bodies are accepted by the backend's request models", () => {
	it.each(STUDIO_TILES.map((tile) => tile.kind))("studio generate: %s with every option value", (kind) => {
		const tile = STUDIO_TILES.find((t) => t.kind === kind);
		const requestName = {
			audio: "AudioRequest",
			video: "VideoRequest",
			report: "ReportRequest",
			slides: "SlidesRequest",
			mindmap: "MindmapRequest",
			flashcards: "StudyRequest",
			quiz: "StudyRequest",
			data_table: "DataTableRequest",
			infographic: "InfographicRequest",
		}[kind];
		const model = component(requestName);
		const properties = model.properties ?? {};
		for (const group of tile?.groups ?? []) {
			expect(Object.keys(properties)).toContain(group.field);
			for (const value of group.values) {
				expect(properties[group.field].enum).toContain(value);
			}
		}
		const state = { ...initialDialogState(kind, [1, 2]), focus: "Focus", language: "es", sourceIds: [1] };
		if (kind === "video") state.options.visual_style = "custom";
		state.stylePrompt = "neon";
		state.templateKey = kind === "report" ? "briefing_doc" : null;
		const body = buildStudioRequest(state, { enabledSourceIds: [1, 2], title: "T", requestId: "r" });
		for (const key of Object.keys(body)) expect(Object.keys(properties)).toContain(key);
	});

	it("new_chat body only uses fields NewChatRequest declares", () => {
		const properties = component("NewChatRequest").properties ?? {};
		const body = buildChatRequest({
			threadId: 1,
			workspaceId: 1,
			query: "q",
			history: [{ role: "user", parts: [{ type: "text", text: "hi" }] }],
			mentions: [{ id: 1, title: "Doc", kind: "doc", document_type: "FILE" }],
		});
		for (const key of Object.keys(body)) expect(Object.keys(properties)).toContain(key);
		const mention = component("MentionedDocumentInfo").properties ?? {};
		for (const key of ["id", "title", "kind", "document_type"]) expect(Object.keys(mention)).toContain(key);
	});

	it("research import items carry only ImportItem fields", () => {
		const properties = component("ImportItem").properties ?? {};
		for (const key of ["url", "title", "snippet", "source"]) expect(Object.keys(properties)).toContain(key);
	});
});
