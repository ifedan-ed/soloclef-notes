/**
 * Settings sections Soloclef Notes keeps in the code but not in front of people.
 *
 * - Hidden for everyone: nothing at the moment (the set stays as the seam).
 * - Administrators only: community prompts (shared with every account),
 *   site announcements (sent to every account) and agent permissions.
 *   API keys are for everyone: "Connect your agent" (shown to every account)
 *   needs one, and the backend lets any signed-in person create their own.
 * The backend refuses the risky writes on its own; this only keeps the pages
 * out of the way of people they are not for.
 */

const HIDDEN_TABS = new Set<string>([]);
const ADMIN_TABS = new Set<string>(["community-prompts", "agent-permissions", "announcements"]);

/** "admin": a site administrator. */
export type Audience = "admin" | "member" | "unknown";

export function audienceOf(isAdmin: boolean | undefined): Audience {
	if (isAdmin === undefined) return "unknown";
	return isAdmin ? "admin" : "member";
}

export function showsSettingsTab(tab: string, who: Audience): boolean {
	if (HIDDEN_TABS.has(tab)) return false;
	if (ADMIN_TABS.has(tab)) return who === "admin";
	return true;
}

/**
 * Whether an address typed straight into the bar should be sent to the default
 * tab. A page nobody sees leaves at once; an administrator's page waits until
 * we know who is looking, or it would bounce away and back.
 */
export function leavesTab(
	tab: string,
	who: Audience,
	shows: (tab: string, who: Audience) => boolean
): boolean {
	if (!shows(tab, "admin")) return true;
	return who !== "unknown" && !shows(tab, who);
}
