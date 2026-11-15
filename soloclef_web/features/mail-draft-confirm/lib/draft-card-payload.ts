import { z } from "zod";

/** The assistant's one mail-writing tool. It drafts; it cannot send. */
export const DRAFT_EMAIL_TOOL = "draft_email";

/**
 * What the server's card allowlist lets through of a finished draft_email
 * call (soloclef_api/internal/chat/cards.go): the draft's id and nothing
 * else. Not the recipients, not the body. The card fetches those from the
 * server, so a saved or shared thread holds no message content, and what is
 * shown is always the stored draft rather than what the model passed in.
 */
const payloadSchema = z
	.object({
		status: z.literal("drafted"),
		// A draft id is the server's UUID; anything else is not one of ours.
		draft_id: z.string().uuid(),
	})
	.strict();
export type DraftCardPayload = z.infer<typeof payloadSchema>;

/**
 * The card for a tool result, or null: a call still running, one that
 * failed, or a shape from another version. Null keeps the call in the turn's
 * trace like any other step (features/chat-messages/timeline/grouping.ts).
 */
export function parseDraftCardPayload(result: unknown): DraftCardPayload | null {
	const parsed = payloadSchema.safeParse(result);
	return parsed.success ? parsed.data : null;
}
