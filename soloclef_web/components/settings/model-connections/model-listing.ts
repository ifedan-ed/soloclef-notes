import type {
	ConnectionHeaders,
	EndpointModelPreviewRequest,
	ModelPreviewRequest,
	ModelSelection,
} from "@/contracts/types/model-connections.types";
import { AppError } from "@/lib/error";

/**
 * Whether a provider's models can be listed when a connection is made, and
 * refreshed afterwards. There are two lists (the provider's `discovery`):
 *
 *  - "proxy_catalogue": the model service's catalogue, every model it knows
 *    of a provider such as OpenAI or Anthropic.
 *  - "endpoint": an OpenAI-compatible endpoint's own GET /models, which the
 *    server asks with the connection's address and key.
 *
 * Any other provider (a local server behind the model service) is in neither,
 * so its models are added by name and proved by the test connecting runs.
 */
export function hasModelCatalogue(provider: { discovery: string } | undefined): boolean {
	return provider?.discovery === "proxy_catalogue" || listsFromEndpoint(provider);
}

/** Whether the list is the endpoint's own, which needs its address and key to be asked. */
export function listsFromEndpoint(provider: { discovery: string } | undefined): boolean {
	return provider?.discovery === "endpoint";
}

/**
 * The providers this account may connect. Any account may add an
 * OpenAI-compatible endpoint of its own (`any_account`); every other
 * provider is the administrator's, and the server refuses it to anyone else,
 * so it is not offered to be refused. An account whose standing is not known
 * yet is shown what any account may do, which only ever grows.
 */
export function providersFor<T extends { any_account: boolean }>(
	providers: readonly T[],
	isAdmin: boolean
): T[] {
	return isAdmin ? [...providers] : providers.filter((provider) => provider.any_account);
}

/** What the connect form has typed that a listing may need. */
export interface PreviewDraft {
	base_url: string | null;
	api_key: string | null;
	headers?: ConnectionHeaders;
}

/**
 * The request that lists a provider's models before the connection exists,
 * or null when it cannot be asked yet.
 *
 * A catalogue is the same whatever the key, so only the provider is sent and
 * no credential leaves the browser for it. An endpoint's own list is asked
 * with the address, key and headers typed; until an address and a key are
 * there, there is nothing to ask (null), and the form says so.
 */
export function previewRequestFor(
	provider: { provider: string; discovery: string } | undefined,
	draft: PreviewDraft
): ModelPreviewRequest | EndpointModelPreviewRequest | null {
	if (!provider || !hasModelCatalogue(provider)) return null;
	if (!listsFromEndpoint(provider)) return { provider: provider.provider };
	const baseUrl = draft.base_url?.trim();
	const apiKey = draft.api_key?.trim();
	if (!baseUrl || !apiKey) return null;
	const request: EndpointModelPreviewRequest = {
		provider: provider.provider,
		base_url: baseUrl,
		api_key: apiKey,
	};
	if (draft.headers && Object.keys(draft.headers).length > 0) request.headers = draft.headers;
	return request;
}

/**
 * Why a listing failed, in the server's own sentence. `typeNames` says the
 * endpoint has no list to give (409 MODEL_LIST_UNSUPPORTED, or the catalogue
 * having none of the provider): the models are then typed by name, which is
 * the one case the form offers that field for. Every other failure (the
 * address refused by the server's egress guard, the key refused, the
 * endpoint unreachable) is shown as said and fixed in the fields above.
 */
export function previewFailureOf(error: unknown): { message: string; typeNames: boolean } {
	const message =
		error instanceof Error && error.message ? error.message : "The models could not be listed.";
	// Both "no list" answers are a 409 (an endpoint's comes coded
	// MODEL_LIST_UNSUPPORTED); no other status means the list does not exist.
	const typeNames = error instanceof AppError && error.status === 409;
	return { message, typeNames };
}

/**
 * The model list with one more model, typed by name. A blank name, and a name
 * already in the list, change nothing: the same list is returned, so a caller
 * can tell by identity.
 */
export function withNamedModel(models: ModelSelection[], name: string): ModelSelection[] {
	const modelId = name.trim();
	if (!modelId || models.some((model) => model.model_id === modelId)) return models;
	return [
		...models,
		{
			model_id: modelId,
			display_name: modelId,
			// Typed by a person, not listed by the model service.
			source: "MANUAL",
			// Naming a model is choosing it.
			enabled: true,
			metadata: {},
		},
	];
}
