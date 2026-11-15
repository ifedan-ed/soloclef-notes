import { z } from "zod";
import { baseApiService } from "@/lib/apis/base-api.service";

const addressSchema = z.object({ name: z.string(), email: z.string() });
export type DraftAddress = z.infer<typeof addressSchema>;

export const draftStatusSchema = z.enum([
	"pending",
	"sending",
	"sent",
	"failed",
	"discarded",
	"superseded",
	"expired",
]);
export type DraftStatus = z.infer<typeof draftStatusSchema>;

/**
 * One stored draft, exactly as the server holds it. The card draws only
 * this: it is the message that would be sent, so nothing the model said
 * about it (its tool arguments, its reply text) is ever shown in its place.
 */
export const mailDraftSchema = z.object({
	id: z.string(),
	status: draftStatusSchema,
	account: z.object({ id: z.string(), label: z.string(), username: z.string() }),
	from: addressSchema,
	to: z.array(addressSchema),
	cc: z.array(addressSchema),
	bcc: z.array(addressSchema),
	subject: z.string(),
	body: z.string(),
	/** Every file the message carries: what goes out, under which name, how large, from where. */
	attachments: z.array(
		z.object({ name: z.string(), type: z.string(), size: z.number(), origin: z.string() })
	),
	reply_to: z.object({ subject: z.string(), from: z.string() }).nullable(),
	/** Recipients who were not on the message this replies to: the ones worth a second look. */
	outside_thread: z.array(z.string()),
	created_at: z.string(),
	expires_at: z.string(),
	sent_at: z.string().nullable(),
	failure: z.string().nullable(),
	superseded_by: z.string().nullable(),
	/** Why a waiting draft cannot be sent now (a file of it changed or was removed). */
	blocked: z.string().nullable(),
});
export type MailDraft = z.infer<typeof mailDraftSchema>;

/**
 * A go-ahead the server issued for this exact content: single use, short
 * lived, this person's. `content_hash` is the server's hash of the message
 * shown; sending posts it back, and the server refuses if the stored draft
 * no longer hashes to it.
 */
/**
 * One thing about a send that is unusual for a person's own mail (many
 * recipients, many sends this hour, recipients never written to, large
 * files). The person ticks each before Send; the server refuses a send that
 * does not acknowledge every one the confirmation was issued with.
 */
export const unusualSchema = z.object({
	code: z.string(),
	/** The server's own sentence, for a code this card does not know. */
	message: z.string(),
	count: z.number().optional(),
	addresses: z.array(z.string()).optional(),
	bytes: z.number().optional(),
	/** The server's threshold the point is past. */
	limit: z.number().optional(),
});
export type UnusualPoint = z.infer<typeof unusualSchema>;

export const confirmationSchema = z.object({
	token: z.string().min(1),
	content_hash: z.string().min(1),
	expires_at: z.string(),
	unusual: z.array(unusualSchema),
});
export type SendConfirmation = z.infer<typeof confirmationSchema>;

export const mailDraftViewSchema = z.object({
	draft: mailDraftSchema,
	confirmation: confirmationSchema.nullable(),
});
export type MailDraftView = z.infer<typeof mailDraftViewSchema>;

const base = "/api/v1/mail/drafts";
const path = (draftId: string, action: string) =>
	`${base}/${encodeURIComponent(draftId)}/${action}`;

/** The three things a person can do with a draft. None of them is the model's. */
export const mailDraftApi = {
	/** The stored draft and, while it can still be sent, a fresh confirmation for it. */
	confirm: (draftId: string) =>
		baseApiService.post(path(draftId, "confirmation"), mailDraftViewSchema),
	/** `acknowledged` is the unusual points the person ticked, by code. */
	send: (draftId: string, confirmation: SendConfirmation, acknowledged: string[]) =>
		baseApiService.post(path(draftId, "send"), mailDraftViewSchema, {
			body: {
				confirmation: confirmation.token,
				content_hash: confirmation.content_hash,
				acknowledged,
			},
		}),
	discard: (draftId: string) => baseApiService.post(path(draftId, "discard"), mailDraftViewSchema),
};
