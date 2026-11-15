import type { MCPOAuthDiscovery } from "@/contracts/types/mcp.types";

/**
 * What OAuth discovery's answer means for the form: whether a client id
 * must be typed, what blocks saving, and the line shown under the choice.
 */
export interface OAuthReadiness {
	needsClientId: boolean;
	/** Why the server cannot be saved with OAuth yet, or null. */
	problem: string | null;
	/** What the form says about the server's sign-in. */
	note: string | null;
}

export function oauthReadiness(
	discovery: MCPOAuthDiscovery | undefined,
	checking: boolean
): OAuthReadiness {
	if (checking && !discovery) {
		return {
			needsClientId: false,
			problem: "Still checking the server's sign-in",
			note: "Checking the server's sign-in…",
		};
	}
	if (!discovery) {
		// Not asked: the URL is not complete yet, and the URL field says so.
		return { needsClientId: false, problem: null, note: null };
	}
	if (discovery.status === "error") {
		return { needsClientId: false, problem: discovery.message, note: null };
	}
	const where = hostOf(discovery.authorization_server);
	return discovery.dynamic_registration
		? {
				needsClientId: false,
				problem: null,
				note: `Signs in at ${where}. This app registers itself there, so no client ID is needed.`,
			}
		: {
				needsClientId: true,
				problem: null,
				note: `Signs in at ${where}, which does not let apps register themselves. Enter the client ID you registered there.`,
			};
}

function hostOf(address: string): string {
	try {
		return new URL(address).host;
	} catch {
		return address;
	}
}
