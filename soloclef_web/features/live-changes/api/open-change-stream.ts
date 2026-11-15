import { authenticatedFetch } from "@/lib/auth-fetch";
import { buildBackendUrl } from "@/lib/env-config";

/**
 * Opens `GET /api/v1/events` with the session, as every other REST read.
 *
 * Through authenticatedFetch rather than EventSource: on a 401 it
 * refreshes the session and asks once more, and EventSource can neither do
 * that nor tell a 401 from a dropped network. A 401 that still comes back is handed to the caller (the connection
 * treats it as signed out) instead of redirecting from inside a background
 * request.
 */
export function openChangeStream(lastEventId: string | null, signal: AbortSignal) {
	const headers: Record<string, string> = { Accept: "text/event-stream" };
	if (lastEventId !== null) headers["Last-Event-ID"] = lastEventId;
	return authenticatedFetch(buildBackendUrl("/api/v1/events"), {
		headers,
		signal,
		cache: "no-store",
		skipAuthRedirect: true,
	});
}
