import { automationDraft } from "@/contracts/types/automation-draft.types";
import { baseApiService } from "./base-api.service";

const BASE = "/api/v1/automation-drafts";

/**
 * A chat draft's two routes: read it, and discard it. Creating one is the
 * ordinary create route with the draft's id (automationsApiService), so the
 * server has one place that saves an automation.
 */
class AutomationDraftsApiService {
	getDraft = async (draftId: string) =>
		baseApiService.get(`${BASE}/${encodeURIComponent(draftId)}`, automationDraft);

	discardDraft = async (draftId: string) =>
		baseApiService.post(`${BASE}/${encodeURIComponent(draftId)}/discard`, automationDraft);
}

export const automationDraftsApiService = new AutomationDraftsApiService();
