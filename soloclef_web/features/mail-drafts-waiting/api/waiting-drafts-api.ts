import { z } from "zod";
import { draftStatusSchema } from "@/features/mail-draft-confirm/api/mail-draft-api";
import { baseApiService } from "@/lib/apis/base-api.service";

/**
 * One line of the Drafts waiting page, as GET /api/v1/mail/drafts lists it
 * (soloclef_api/internal/mailconfirm). It names the draft and nothing more:
 * the card drawn for it fetches the draft itself, with its own single-use
 * confirmation, so the recipients and the body are never in this list.
 */
export const waitingDraftSchema = z.object({
	id: z.string(),
	status: draftStatusSchema,
	subject: z.string(),
	/** The mailbox it would be sent from, as its owner named it; "" if unnamed. */
	account_label: z.string(),
	created_at: z.string(),
	expires_at: z.string(),
});
export type WaitingDraft = z.infer<typeof waitingDraftSchema>;

export const waitingDraftsSchema = z.object({
	drafts: z.array(waitingDraftSchema),
	/** More are waiting than the server lists at once (fifty). */
	more: z.boolean(),
});
export type WaitingDrafts = z.infer<typeof waitingDraftsSchema>;

export const waitingDraftsKey = ["mail", "drafts", "waiting"] as const;

export const waitingDraftsApi = {
	list: () => baseApiService.get("/api/v1/mail/drafts", waitingDraftsSchema),
};
