import { z } from "zod";
import { automationCreateRequest } from "./automation.types";

/**
 * An automation the chat's create_automation tool drafted, as
 * GET /api/v1/automation-drafts/{id} answers it
 * (soloclef_api/internal/automationdrafts).
 */
export const automationDraftStatus = z.enum(["pending", "created", "discarded", "superseded"]);
export type AutomationDraftStatus = z.infer<typeof automationDraftStatus>;

export const automationDraft = z.object({
	id: z.string().uuid(),
	status: automationDraftStatus,
	/** Exactly the body POST /api/v1/automations takes: the card edits a copy and sends it. */
	request: automationCreateRequest,
	/** The automation Create made; null until then, and after it was deleted. */
	automation_id: z.number().nullable(),
	/** The newer draft the chat made when the person asked for a change in words. */
	replaced_by: z.string().nullable(),
	/**
	 * What Create saved, as it now is: the person may have changed any field
	 * on the card first, so a created card shows this and not the request.
	 * Null until created, and after the automation was deleted.
	 */
	automation: z
		.object({
			name: z.string(),
			triggers: z.array(z.object({ type: z.string(), params: z.record(z.string(), z.any()) })),
		})
		.nullable(),
	/** The folders the draft's (or its automation's) event trigger waits on, by name. */
	folders: z.array(z.object({ id: z.number(), title: z.string() })),
	created_at: z.string(),
});
export type AutomationDraft = z.infer<typeof automationDraft>;
