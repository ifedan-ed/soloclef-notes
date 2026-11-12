import {
	type PromptCreateRequest,
	type PromptUpdateRequest,
	promptCreateRequest,
	promptDeleteResponse,
	promptRead,
	promptsListResponse,
	promptUpdateRequest,
	publicPromptsListResponse,
} from "@/contracts/types/prompts.types";
import { ValidationError } from "@/lib/error";
import { baseApiService } from "./base-api.service";

class PromptsApiService {
	// The signed-in account's own prompts.
	list = async () => {
		return baseApiService.get("/api/v1/prompts", promptsListResponse);
	};

	create = async (request: PromptCreateRequest) => {
		const parsed = promptCreateRequest.safeParse(request);
		if (!parsed.success) {
			const errorMessage = parsed.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		return baseApiService.post("/api/v1/prompts", promptRead, {
			body: parsed.data,
		});
	};

	update = async (promptId: number, request: PromptUpdateRequest) => {
		const parsed = promptUpdateRequest.safeParse(request);
		if (!parsed.success) {
			const errorMessage = parsed.error.issues.map((issue) => issue.message).join(", ");
			throw new ValidationError(`Invalid request: ${errorMessage}`);
		}

		return baseApiService.put(`/api/v1/prompts/${promptId}`, promptRead, {
			body: parsed.data,
		});
	};

	delete = async (promptId: number) => {
		return baseApiService.delete(`/api/v1/prompts/${promptId}`, promptDeleteResponse);
	};

	listPublic = async () => {
		return baseApiService.get("/api/v1/prompts/public", publicPromptsListResponse);
	};

	copy = async (promptId: number) => {
		return baseApiService.post(`/api/v1/prompts/${promptId}/copy`, promptRead, {});
	};
}

export const promptsApiService = new PromptsApiService();
