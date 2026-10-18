import { authenticatedFetch } from "@/lib/auth-fetch";
import { buildBackendUrl } from "@/lib/env-config";

/** What the backend says about a thread's turn (``GET /threads/{id}/turn-status``). */
export type ServerTurnStatus = "idle" | "busy" | "cancelling";

export function parseServerTurnStatus(value: unknown): ServerTurnStatus | null {
	if (typeof value !== "object" || value === null) return null;
	const status = (value as { status?: unknown }).status;
	return status === "idle" || status === "busy" || status === "cancelling" ? status : null;
}

export async function fetchServerTurnStatus(threadId: number): Promise<ServerTurnStatus> {
	const response = await authenticatedFetch(
		buildBackendUrl(`/api/v1/threads/${threadId}/turn-status`)
	);
	if (!response.ok) {
		throw new Error(`turn-status ${response.status}`);
	}
	const status = parseServerTurnStatus(await response.json());
	if (!status) throw new Error("turn-status: unexpected reply");
	return status;
}

/**
 * Stop the thread's running turn on the server, whichever page started it.
 * The backend cancels the turn, saves what it wrote so far and answers.
 */
export async function requestServerStop(threadId: number): Promise<void> {
	const response = await authenticatedFetch(
		buildBackendUrl(`/api/v1/threads/${threadId}/cancel-active-turn`),
		{ method: "POST" }
	);
	if (!response.ok) {
		throw new Error(`cancel-active-turn ${response.status}`);
	}
}
