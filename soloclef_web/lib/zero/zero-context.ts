import type { Context } from "@/types/zero";

/**
 * Zero's context for a signed-in account, read from the API's `/users/me`.
 *
 * The context is only who is asking: every synced row is that account's own
 * (zero/queries/ownership.ts), so there is nothing else to send and no
 * endpoint of Zero's own to keep in step with the API. The browser asks with
 * its session; the query route asks with the credential zero-cache forwards.
 */
export const ZERO_CONTEXT_PATH = "/users/me";

/** The context in the account read, or null when the answer names no account. */
export function zeroContextFromAccount(account: unknown): Exclude<Context, undefined> | null {
	const id = (account as { id?: unknown } | null)?.id;
	return typeof id === "string" && id.length > 0 ? { userId: id } : null;
}
