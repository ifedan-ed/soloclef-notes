/**
 * This tab's name in the presence beats: random, made once per page load
 * and kept in memory only, so two tabs (or two windows) are two marks and
 * hiding one does not end the other's. 32 hex characters, inside the
 * server's 8 to 64 of [A-Za-z0-9_-].
 *
 * crypto.getRandomValues, not randomUUID: the latter exists only on https
 * and localhost, and a self-hosted site may be served over plain http.
 */
let tabClientId: string | null = null;

export function presenceClientId(): string {
	if (tabClientId === null) tabClientId = randomClientId();
	return tabClientId;
}

export function randomClientId(): string {
	const bytes = crypto.getRandomValues(new Uint8Array(16));
	return Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0")).join("");
}
