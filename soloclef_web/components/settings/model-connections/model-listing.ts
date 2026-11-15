import type {
	ConnectionHeaders,
	EndpointModelPreviewRequest,
	ModelSelection,
} from "@/contracts/types/model-connections.types";
import { AppError } from "@/lib/error";
import { OWN_PROVIDER } from "./connection-presets";

/** What the connect form has typed that a listing needs. */
export interface PreviewDraft {
	base_url: string | null;
	api_key: string | null;
	headers?: ConnectionHeaders;
}

/**
 * The request that lists an endpoint's models before the connection exists,
 * or null when it cannot be asked yet.
 *
 * The list is the endpoint's own GET /models, which the server asks with the
 * address, key and headers typed (soloclef_api/internal/modelconnections,
 * endpoint.go). Until an address and a key are there, there is nothing to
 * ask, and the form's List models button stays off.
 */
export function previewRequestFor(draft: PreviewDraft): EndpointModelPreviewRequest | null {
	const baseUrl = draft.base_url?.trim();
	const apiKey = draft.api_key?.trim();
	if (!baseUrl || !apiKey) return null;
	const request: EndpointModelPreviewRequest = {
		provider: OWN_PROVIDER,
		base_url: baseUrl,
		api_key: apiKey,
	};
	if (draft.headers && Object.keys(draft.headers).length > 0) request.headers = draft.headers;
	return request;
}

/**
 * Why a listing failed, in the server's own sentence. `typeNames` says the
 * endpoint has no list to give (409 MODEL_LIST_UNSUPPORTED): the models are
 * then typed by name, which is the one case the form offers that field for.
 * Every other failure (the address refused by the server's egress guard, the
 * key refused, the endpoint unreachable) is shown as said and fixed in the
 * fields above.
 */
export function previewFailureOf(error: unknown): { message: string; typeNames: boolean } {
	const message =
		error instanceof Error && error.message ? error.message : "The models could not be listed.";
	// "No list" is a 409 (coded MODEL_LIST_UNSUPPORTED); no other status
	// means the list does not exist.
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
			// Typed by a person, not listed by the endpoint.
			source: "MANUAL",
			// Naming a model is choosing it.
			enabled: true,
			metadata: {},
		},
	];
}
