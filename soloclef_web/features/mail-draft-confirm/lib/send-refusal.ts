import { AppError } from "@/lib/error";

/**
 * What the card does after a send the server refused, by the refusal's code
 * (soloclef_api/internal/mailconfirm). Every path ends with the person
 * looking at the draft again; none of them sends by itself.
 *
 *   reconfirm  fetch the draft and a new confirmation, and ask the person to
 *              check it and press Send again (the old go-ahead is spent or
 *              stale, or the draft itself changed)
 *   refresh    fetch the draft to show its real state (sent elsewhere,
 *              discarded, replaced, expired): no new Send is offered unless
 *              the server issues a confirmation
 *   stay       keep the card as it is and say why (a failed send)
 */
export type SendRefusal = {
	next: "reconfirm" | "refresh" | "stay";
	/** Key in the `mail_draft` messages. */
	messageKey: string;
	/** The server's own sentence, for a failure only it can word. */
	detail?: string;
};

export function sendRefusal(error: unknown): SendRefusal {
	if (!(error instanceof AppError)) return { next: "stay", messageKey: "send_failed_unknown" };
	// A 404 arrives without its code (the client's NotFoundError): the draft
	// is gone or was never this person's, which is the same thing to them.
	if (error.status === 404) return { next: "refresh", messageKey: "draft_gone" };
	switch (error.code) {
		case "draft_changed":
			return { next: "reconfirm", messageKey: "draft_changed" };
		case "confirmation_expired":
			return { next: "reconfirm", messageKey: "confirmation_expired" };
		case "confirmation_spent":
			return { next: "reconfirm", messageKey: "confirmation_spent" };
		case "draft_not_pending":
			return { next: "refresh", messageKey: "draft_not_pending" };
		// The card asks for every tick before Send, so this is a confirmation
		// drawn before an unusual point existed: shown again, with the point.
		case "unusual_not_confirmed":
			return { next: "reconfirm", messageKey: "unusual_not_confirmed" };
		case "unusual_changed":
			return { next: "reconfirm", messageKey: "unusual_changed" };
		case "send_failed":
			return { next: "refresh", messageKey: "send_failed", detail: error.message };
		default:
			return { next: "stay", messageKey: "send_failed_unknown" };
	}
}
