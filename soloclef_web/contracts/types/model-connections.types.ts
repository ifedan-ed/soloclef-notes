import { z } from "zod";

// Whose a connection is: "user" is the signed-in account's own, "global" is
// one the site shares with everyone. Nothing else can own one.
export const connectionScopeEnum = z.enum(["global", "user"]);
export const modelSourceEnum = z.enum(["DISCOVERED", "MANUAL"]);

export const modelRead = z.object({
	id: z.number(),
	connection_id: z.number(),
	model_id: z.string(),
	display_name: z.string().nullable().optional(),
	source: z.union([modelSourceEnum, z.string()]),
	supports_chat: z.boolean().nullable().optional(),
	max_input_tokens: z.number().nullable().optional(),
	supports_image_input: z.boolean().nullable().optional(),
	supports_tools: z.boolean().nullable().optional(),
	supports_image_generation: z.boolean().nullable().optional(),
	capabilities_override: z.record(z.string(), z.any()).default({}),
	enabled: z.boolean(),
	catalog: z.record(z.string(), z.any()).default({}),
	created_at: z.string().nullable().optional(),
});

// A connection as the server answers it. There is no `api_key` here on
// purpose: the server never sends a stored key back, to anybody. `has_api_key`
// says whether one is saved, and that is all the browser is told. `extra`
// arrives without the credentials some providers keep in it (Bedrock, Vertex).
// So nothing read from a connection may be sent back as a credential: an
// update leaves `api_key` out to keep the saved key, and a change is tested
// with `connectionTestUpdateRequest`, where the server uses the key it holds.
export const connectionRead = z.object({
	id: z.number(),
	provider: z.string(),
	base_url: z.string().nullable().optional(),
	extra: z.record(z.string(), z.any()).default({}),
	// Exactly the enum, with no free-text escape, so a comparison against a
	// value the server no longer sends does not compile.
	scope: connectionScopeEnum,
	user_id: z.string().nullable().optional(),
	enabled: z.boolean(),
	has_api_key: z.boolean(),
	// The names of the extra headers an OpenAI-compatible connection sends,
	// sorted. Their values are never answered, for the reason the key is
	// not: a header can carry a credential. Absent reads as none, as an
	// absent `models` does, because the site's shared connections carry none.
	header_names: z.array(z.string()).default([]),
	models: z.array(modelRead).default([]),
	created_at: z.string().nullable().optional(),
});

export const modelSelection = z.object({
	model_id: z.string().min(1),
	display_name: z.string().nullable().optional(),
	source: z.union([modelSourceEnum, z.string()]).default("DISCOVERED"),
	supports_chat: z.boolean().nullable().optional(),
	max_input_tokens: z.number().nullable().optional(),
	supports_image_input: z.boolean().nullable().optional(),
	supports_tools: z.boolean().nullable().optional(),
	supports_image_generation: z.boolean().nullable().optional(),
	enabled: z.boolean().default(false),
	metadata: z.record(z.string(), z.any()).default({}),
});

export const modelPreviewRead = modelSelection;

// Extra headers an OpenAI-compatible endpoint wants (an organisation or a
// gateway's own key header), name to value. Only that provider takes them;
// the server refuses the names it sets itself (the web checks first:
// components/settings/model-connections/connection-headers.ts).
export const connectionHeaders = z.record(z.string(), z.string());

export const connectionCreateRequest = z.object({
	provider: z.string().min(1),
	base_url: z.string().nullable().optional(),
	api_key: z.string().nullable().optional(),
	headers: connectionHeaders.nullable().optional(),
	extra: z.record(z.string(), z.any()).default({}),
	scope: connectionScopeEnum,
	enabled: z.boolean().default(true),
	models: z.array(modelSelection).default([]),
});

// Listing a provider's models before a connection is made, from the model
// service's catalogue. Only the provider is sent: the catalogue is the same
// whatever the key and the address, so neither leaves the browser for it.
export const modelPreviewRequest = z.object({
	provider: z.string().min(1),
});

// Listing the models of an OpenAI-compatible endpoint before a connection is
// made. Here the list is the endpoint's own (its GET /models), which the
// server asks with the address, key and headers typed, so all three are sent.
export const endpointModelPreviewRequest = z.object({
	provider: z.string().min(1),
	base_url: z.string().min(1),
	api_key: z.string().min(1),
	headers: connectionHeaders.optional(),
});

export const modelTestPreviewRequest = connectionCreateRequest.extend({
	model_id: z.string().min(1),
});

export const connectionUpdateRequest = z.object({
	provider: z.string().nullable().optional(),
	base_url: z.string().nullable().optional(),
	api_key: z.string().nullable().optional(),
	// Absent keeps the saved headers, an object replaces them all, and null
	// removes them: the saved values are never in the browser to send back.
	headers: connectionHeaders.nullable().optional(),
	extra: z.record(z.string(), z.any()).optional(),
	enabled: z.boolean().optional(),
});

// A change to a saved connection, tested before it is saved. It reads as the
// update that follows it does: a field that is present is the proposed value
// (null included, so `api_key: null` tests with no key), and a field that is
// absent is the saved one. Leaving `api_key` out is how a connection is tested
// with a key the browser does not have.
export const connectionTestUpdateRequest = connectionUpdateRequest.extend({
	model_id: z.string().min(1),
});

export const modelUpdateRequest = z.object({
	display_name: z.string().nullable().optional(),
	enabled: z.boolean().optional(),
	supports_chat: z.boolean().nullable().optional(),
	max_input_tokens: z.number().positive().nullable().optional(),
	supports_image_input: z.boolean().nullable().optional(),
	supports_tools: z.boolean().nullable().optional(),
	supports_image_generation: z.boolean().nullable().optional(),
	capabilities_override: z.record(z.string(), z.any()).optional(),
});

export const modelsBulkUpdateRequest = z.object({
	model_ids: z.array(z.number()).min(1).max(1000),
	enabled: z.boolean(),
});

export const verifyConnectionResponse = z.object({
	status: z.string(),
	ok: z.boolean(),
	message: z.string().default(""),
});

export const globalLlmConfigStatus = z.object({
	exists: z.boolean(),
});

export const llmSetupStatus = z.object({
	status: z.enum(["ready", "needs_setup"]),
	source: z.enum(["global_config", "models", "none"]),
	can_configure: z.boolean(),
	stage: z.enum(["initial_setup", "recovery", "ready"]),
});

export const modelProviderRead = z.object({
	provider: z.string(),
	transport: z.string(),
	// Where the provider's models are listed from: "proxy_catalogue" when the
	// model service has a list of them, "endpoint" when the server asks the
	// connection's own address (an OpenAI-compatible endpoint's GET /models),
	// "none" when neither (each model is added by its name).
	discovery: z.string(),
	// Whether any signed-in account may connect this provider. Only an
	// OpenAI-compatible endpoint, which the server calls directly behind its
	// egress guard, is; every other provider is the administrator's.
	any_account: z.boolean(),
	default_base_url: z.string().nullable().optional(),
	base_url_required: z.boolean(),
	auth_style: z.string(),
	local_only: z.boolean().default(false),
});

export const modelProviderListResponse = z.array(modelProviderRead);

export const connectionListResponse = z.array(connectionRead);
export const modelListResponse = z.array(modelRead);
export const modelPreviewListResponse = z.array(modelPreviewRead);

export type ConnectionScope = z.infer<typeof connectionScopeEnum>;
export type ModelRead = z.infer<typeof modelRead>;
export type ModelPreviewRead = z.infer<typeof modelPreviewRead>;
export type ModelSelection = z.infer<typeof modelSelection>;
export type ConnectionRead = z.infer<typeof connectionRead>;
export type ConnectionCreateRequest = z.infer<typeof connectionCreateRequest>;
export type ModelPreviewRequest = z.infer<typeof modelPreviewRequest>;
export type EndpointModelPreviewRequest = z.infer<typeof endpointModelPreviewRequest>;
export type ConnectionHeaders = z.infer<typeof connectionHeaders>;
export type ModelTestPreviewRequest = z.infer<typeof modelTestPreviewRequest>;
export type ConnectionUpdateRequest = z.infer<typeof connectionUpdateRequest>;
export type ConnectionTestUpdateRequest = z.infer<typeof connectionTestUpdateRequest>;
export type ModelUpdateRequest = z.infer<typeof modelUpdateRequest>;
export type ModelsBulkUpdateRequest = z.infer<typeof modelsBulkUpdateRequest>;
export type GlobalLlmConfigStatus = z.infer<typeof globalLlmConfigStatus>;
export type LlmSetupStatus = z.infer<typeof llmSetupStatus>;
export type VerifyConnectionResponse = z.infer<typeof verifyConnectionResponse>;
export type ModelProviderRead = z.infer<typeof modelProviderRead>;
