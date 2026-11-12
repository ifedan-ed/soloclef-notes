import {
	type ConnectionCreateRequest,
	type ConnectionRead,
	type ConnectionTestUpdateRequest,
	type ConnectionUpdateRequest,
	connectionCreateRequest,
	connectionListResponse,
	connectionRead,
	connectionTestUpdateRequest,
	connectionUpdateRequest,
	type EndpointModelPreviewRequest,
	endpointModelPreviewRequest,
	type GlobalLlmConfigStatus,
	globalLlmConfigStatus,
	type LlmSetupStatus,
	llmSetupStatus,
	type ModelPreviewRead,
	type ModelPreviewRequest,
	type ModelProviderRead,
	type ModelRead,
	type ModelsBulkUpdateRequest,
	type ModelTestPreviewRequest,
	type ModelUpdateRequest,
	modelListResponse,
	modelPreviewListResponse,
	modelPreviewRequest,
	modelProviderListResponse,
	modelRead,
	modelsBulkUpdateRequest,
	modelTestPreviewRequest,
	modelUpdateRequest,
	type VerifyConnectionResponse,
	verifyConnectionResponse,
} from "@/contracts/types/model-connections.types";
import { ValidationError } from "../error";
import { baseApiService } from "./base-api.service";

class ModelConnectionsApiService {
	getGlobalConnections = async (): Promise<ConnectionRead[]> => {
		return baseApiService.get(`/api/v1/global-model-connections`, connectionListResponse);
	};

	getGlobalLlmConfigStatus = async (): Promise<GlobalLlmConfigStatus> => {
		return baseApiService.get(`/api/v1/global-llm-config-status`, globalLlmConfigStatus);
	};

	// Whether the signed-in account has a chat model it can use.
	getLlmSetupStatus = async (): Promise<LlmSetupStatus> => {
		return baseApiService.get(`/api/v1/users/me/llm-setup-status`, llmSetupStatus);
	};

	getModelProviders = async (): Promise<ModelProviderRead[]> => {
		return baseApiService.get(`/api/v1/model-providers`, modelProviderListResponse);
	};

	// The signed-in account's own connections (the shared ones are
	// getGlobalConnections).
	getConnections = async (): Promise<ConnectionRead[]> => {
		return baseApiService.get(`/api/v1/model-connections`, connectionListResponse);
	};

	createConnection = async (request: ConnectionCreateRequest): Promise<ConnectionRead> => {
		const parsed = connectionCreateRequest.safeParse(request);
		if (!parsed.success) {
			throw new ValidationError(parsed.error.issues.map((issue) => issue.message).join(", "));
		}
		return baseApiService.post(`/api/v1/model-connections`, connectionRead, {
			body: parsed.data,
		});
	};

	updateConnection = async (
		id: number,
		request: ConnectionUpdateRequest
	): Promise<ConnectionRead> => {
		const parsed = connectionUpdateRequest.safeParse(request);
		if (!parsed.success) {
			throw new ValidationError(parsed.error.issues.map((issue) => issue.message).join(", "));
		}
		return baseApiService.put(`/api/v1/model-connections/${id}`, connectionRead, {
			body: parsed.data,
		});
	};

	deleteConnection = async (id: number) => {
		return baseApiService.delete(`/api/v1/model-connections/${id}`, undefined);
	};

	verifyConnection = async (id: number): Promise<VerifyConnectionResponse> => {
		return baseApiService.post(`/api/v1/model-connections/${id}/verify`, verifyConnectionResponse);
	};

	discoverModels = async (id: number): Promise<ModelRead[]> => {
		return baseApiService.post(`/api/v1/model-connections/${id}/discover`, modelListResponse);
	};

	// A provider's models before a connection is made. A catalogue request
	// (just the provider) is parsed by the catalogue's schema, which drops
	// any key or address, because the catalogue does not depend on them. An
	// endpoint's request carries the address, key and headers its own
	// GET /models is asked with (model-listing.ts, previewRequestFor).
	previewModels = async (
		request: ModelPreviewRequest | EndpointModelPreviewRequest
	): Promise<ModelPreviewRead[]> => {
		const parsed =
			"base_url" in request
				? endpointModelPreviewRequest.safeParse(request)
				: modelPreviewRequest.safeParse(request);
		if (!parsed.success) {
			throw new ValidationError(parsed.error.issues.map((issue) => issue.message).join(", "));
		}
		return baseApiService.post(
			`/api/v1/model-connections/discover-preview`,
			modelPreviewListResponse,
			{ body: parsed.data }
		);
	};

	testPreviewModel = async (
		request: ModelTestPreviewRequest
	): Promise<VerifyConnectionResponse> => {
		const parsed = modelTestPreviewRequest.safeParse(request);
		if (!parsed.success) {
			throw new ValidationError(parsed.error.issues.map((issue) => issue.message).join(", "));
		}
		return baseApiService.post(`/api/v1/model-connections/test-preview`, verifyConnectionResponse, {
			body: parsed.data,
		});
	};

	// Tests a saved connection as it would be after `request` is applied,
	// saving nothing. The parsed body is sent as it is: zod leaves an absent
	// optional field absent, and an absent `api_key` is what tells the server
	// to test with the saved key.
	testConnectionUpdate = async (
		id: number,
		request: ConnectionTestUpdateRequest
	): Promise<VerifyConnectionResponse> => {
		const parsed = connectionTestUpdateRequest.safeParse(request);
		if (!parsed.success) {
			throw new ValidationError(parsed.error.issues.map((issue) => issue.message).join(", "));
		}
		return baseApiService.post(
			`/api/v1/model-connections/${id}/test-update`,
			verifyConnectionResponse,
			{ body: parsed.data }
		);
	};

	updateModel = async (id: number, request: ModelUpdateRequest): Promise<ModelRead> => {
		const parsed = modelUpdateRequest.safeParse(request);
		if (!parsed.success) {
			throw new ValidationError(parsed.error.issues.map((issue) => issue.message).join(", "));
		}
		return baseApiService.put(`/api/v1/models/${id}`, modelRead, {
			body: parsed.data,
		});
	};

	bulkUpdateModels = async (
		connectionId: number,
		request: ModelsBulkUpdateRequest
	): Promise<ModelRead[]> => {
		const parsed = modelsBulkUpdateRequest.safeParse(request);
		if (!parsed.success) {
			throw new ValidationError(parsed.error.issues.map((issue) => issue.message).join(", "));
		}
		return baseApiService.request(
			`/api/v1/model-connections/${connectionId}/models`,
			modelListResponse,
			{
				method: "PATCH",
				headers: { "Content-Type": "application/json" },
				body: parsed.data,
			}
		);
	};

	testModel = async (id: number): Promise<VerifyConnectionResponse> => {
		return baseApiService.post(`/api/v1/models/${id}/test`, verifyConnectionResponse);
	};
}

export const modelConnectionsApiService = new ModelConnectionsApiService();
