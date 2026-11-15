import { buildBackendUrl } from "@/lib/env-config";

/**
 * One presence beat (PUT /api/v1/presence, 204). Sent with plain fetch and
 * `keepalive`, not through the API client: the "gone" beat goes out as the
 * page is hidden or closed, and only a keepalive request outlives the page.
 * It is signed the way the API client signs every request: by the session
 * cookie (credentials "include").
 *
 * A beat that fails is dropped without a word: it is a hint to hold back
 * phone pushes, never something the person needs to hear about, and no
 * refresh or retry is worth a request on its way out of a closing page.
 */
export function sendPresence(clientId: string, active: boolean): void {
	try {
		fetch(buildBackendUrl("/api/v1/presence"), {
			method: "PUT",
			keepalive: true,
			credentials: "include",
			headers: { "Content-Type": "application/json" },
			body: JSON.stringify({ client_id: clientId, active }),
		}).catch(() => {});
	} catch {
		// fetch itself can throw synchronously (a keepalive body over the browser's quota): still only a hint.
	}
}
