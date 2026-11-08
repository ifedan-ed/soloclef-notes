import {
	type AutomationCreateRequest,
	type AutomationListParams,
	type AutomationUpdateRequest,
	automation,
	automationCreateRequest,
	automationListResponse,
	automationUpdateRequest,
	modelEligibility,
	type RunListParams,
	run,
	runListResponse,
	runSummary,
	type TriggerCreateRequest,
	type TriggerUpdateRequest,
	trigger,
	triggerCreateRequest,
	triggerUpdateRequest,
} from "@/contracts/types/automation.types";
import { ValidationError } from "../error";
import { baseApiService } from "./base-api.service";

const BASE = "/api/v1/automations";

function rejectIfInvalid<T>(
	parsed: { success: true; data: T } | { success: false; error: { issues: { message: string }[] } }
): T {
	if (!parsed.success) {
		throw new ValidationError(
			`Invalid request: ${parsed.error.issues.map((i) => i.message).join(", ")}`
		);
	}
	return parsed.data;
}

class AutomationsApiService {
	// ---- Automations ---------------------------------------------------------

	listAutomations = async (params: AutomationListParams) => {
		const qs = new URLSearchParams({
			workspace_id: String(params.workspace_id),
			limit: String(params.limit),
			offset: String(params.offset),
		});
		return baseApiService.get(`${BASE}?${qs.toString()}`, automationListResponse);
	};

	getAutomation = async (automationId: number) => {
		return baseApiService.get(`${BASE}/${automationId}`, automation);
	};

	createAutomation = async (request: AutomationCreateRequest) => {
		const data = rejectIfInvalid(automationCreateRequest.safeParse(request));
		const { workspace_id, ...body } = data;
		return baseApiService.post(BASE, automation, {
			body: { ...body, workspace_id },
		});
	};

	updateAutomation = async (automationId: number, request: AutomationUpdateRequest) => {
		const data = rejectIfInvalid(automationUpdateRequest.safeParse(request));
		return baseApiService.patch(`${BASE}/${automationId}`, automation, { body: data });
	};

	// Server returns 204; baseApiService now resolves to null and skips schema validation.
	deleteAutomation = async (automationId: number) => {
		return baseApiService.delete(`${BASE}/${automationId}`);
	};

	// Model eligibility for automations. Nothing is refused any more — the
	// endpoint reports every slot as allowed — but it is still the shape the
	// backend serves, and the one place a future rule would surface.
	getModelEligibility = async (workspaceId: number) => {
		const qs = new URLSearchParams({ workspace_id: String(workspaceId) });
		return baseApiService.get(`${BASE}/model-eligibility?${qs.toString()}`, modelEligibility);
	};

	// ---- Triggers (sub-resource) --------------------------------------------

	addTrigger = async (automationId: number, request: TriggerCreateRequest) => {
		const data = rejectIfInvalid(triggerCreateRequest.safeParse(request));
		return baseApiService.post(`${BASE}/${automationId}/triggers`, trigger, { body: data });
	};

	updateTrigger = async (
		automationId: number,
		triggerId: number,
		request: TriggerUpdateRequest
	) => {
		const data = rejectIfInvalid(triggerUpdateRequest.safeParse(request));
		return baseApiService.patch(`${BASE}/${automationId}/triggers/${triggerId}`, trigger, {
			body: data,
		});
	};

	removeTrigger = async (automationId: number, triggerId: number) => {
		return baseApiService.delete(`${BASE}/${automationId}/triggers/${triggerId}`);
	};

	// ---- Runs (sub-resource: history, plus cancel) ---------------------------

	listRuns = async (automationId: number, params: RunListParams) => {
		const qs = new URLSearchParams({
			limit: String(params.limit),
			offset: String(params.offset),
		});
		return baseApiService.get(`${BASE}/${automationId}/runs?${qs.toString()}`, runListResponse);
	};

	getRun = async (automationId: number, runId: number) => {
		return baseApiService.get(`${BASE}/${automationId}/runs/${runId}`, run);
	};

	// Cancels a pending or running run; 409 ("This run already finished.") otherwise.
	cancelRun = async (automationId: number, runId: number) => {
		return baseApiService.post(`${BASE}/${automationId}/runs/${runId}/cancel`, runSummary);
	};
}

export const automationsApiService = new AutomationsApiService();
