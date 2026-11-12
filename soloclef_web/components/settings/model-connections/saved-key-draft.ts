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
 *  - keep:    the field was left empty. The saved key (or the absence of one)
 *             stays as it is, and `api_key` is not sent at all.
 *  - replace: a key was typed. That key is sent.
 *  - remove:  "Remove saved key" was pressed. `api_key: null` is sent.
 */
export type SavedKeyIntent =
	| { kind: "keep" }
	| { kind: "replace"; apiKey: string }
	| { kind: "remove" };

/**
 * A typed key always wins: typing after asking for removal means "use this
 * one instead". Whitespace alone is not a key.
 */
export function savedKeyIntent(typedKey: string, removeRequested: boolean): SavedKeyIntent {
	const apiKey = typedKey.trim();
	if (apiKey) return { kind: "replace", apiKey };
	if (removeRequested) return { kind: "remove" };
	return { kind: "keep" };
}

/**
 * The update for a saved connection. `api_key` is present only when the key
 * is being replaced or removed; for "keep" the property does not exist, which
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
	if (intent.kind === "remove") data.api_key = null;
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
