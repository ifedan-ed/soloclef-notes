import type { ConnectionUpdateRequest } from "@/contracts/types/model-connections.types";
import type { SavedHeadersIntent } from "./connection-headers";

const KEEP_HEADERS: SavedHeadersIntent = { kind: "keep" };

/**
 * What the person editing a saved connection wants done with its API key.
 *
 * The server never sends a saved key back, so the editor cannot compare what
 * is in the field with what is stored, and an empty field cannot mean "the key
 * is now empty". Each thing that can be wanted is its own case instead:
 *
 *  - keep:    the field was left empty. The saved key stays as it is, and
 *             `api_key` is not sent at all.
 *  - replace: a key was typed. That key is sent.
 *
 * There is no "remove": an OpenAI-compatible endpoint, the one kind of
 * connection there is, is called with its key, and the server refuses a
 * connection without one. Offering removal would only offer that refusal.
 */
export type SavedKeyIntent = { kind: "keep" } | { kind: "replace"; apiKey: string };

/** Whitespace alone is not a key. */
export function savedKeyIntent(typedKey: string): SavedKeyIntent {
	const apiKey = typedKey.trim();
	return apiKey ? { kind: "replace", apiKey } : { kind: "keep" };
}

/**
 * The update for a saved connection. `api_key` is present only when the key
 * is being replaced; for "keep" the property does not exist, which
 * is what tells the server to leave the saved key alone. Nothing read from
 * the connection is ever put in `api_key`. The extra headers of an
 * OpenAI-compatible connection follow the same rule (./connection-headers):
 * `headers` is sent only to replace or remove them.
 */
export function connectionUpdateFor(
	baseUrlDraft: string,
	intent: SavedKeyIntent,
	headers: SavedHeadersIntent = KEEP_HEADERS
): ConnectionUpdateRequest {
	const data: ConnectionUpdateRequest = { base_url: baseUrlDraft.trim() || null };
	if (intent.kind === "replace") data.api_key = intent.apiKey;
	if (headers.kind === "replace") data.headers = headers.headers;
	if (headers.kind === "remove") data.headers = null;
	return data;
}

/** Whether the connection's own settings (not its models) have been changed. */
export function hasConnectionChanges(
	savedBaseUrl: string | null | undefined,
	baseUrlDraft: string,
	intent: SavedKeyIntent,
	headers: SavedHeadersIntent = KEEP_HEADERS
): boolean {
	return (
		baseUrlDraft.trim() !== (savedBaseUrl ?? "") ||
		intent.kind !== "keep" ||
		headers.kind !== "keep"
	);
}
