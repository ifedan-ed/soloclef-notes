/**
 * Settings sections Soloclef Notes keeps in the code but not in front of people.
 *
 * - Hidden for everyone: nothing at the moment (the set stays as the seam).
 * - Administrators only: community prompts (shared with every account),
 *   site announcements (sent to every account) and agent permissions.
 *   API keys are for everyone: "Connect your agent" (shown to every account)
 *   needs one, and the backend lets any signed-in person create their own.
 * - Workspace settings: none are Owner-only now. The connected git
 *   repository page was removed (git storage is off; its old address
 *   redirects to General), so the Owner-only set is empty but kept as the seam.
 * The backend refuses the risky writes on its own; this only keeps the pages
 * out of the way. For workspace settings the privileged audience is the
 * workspace Owner; for user settings it is an administrator.
 */

const HIDDEN_USER_TABS = new Set<string>([]);
const ADMIN_USER_TABS = new Set<string>([
	"community-prompts",
	"agent-permissions",
	"announcements",
]);
const OWNER_WORKSPACE_TABS = new Set<string>([]);

/** "admin": an administrator (user settings) or the workspace Owner (workspace settings). */
export type Audience = "admin" | "member" | "unknown";

export function audienceOf(isPrivileged: boolean | undefined): Audience {
	if (isPrivileged === undefined) return "unknown";
	return isPrivileged ? "admin" : "member";
}

function shown(tab: string, adminTabs: Set<string>, hidden: Set<string>, who: Audience) {
	if (hidden.has(tab)) return false;
	if (adminTabs.has(tab)) return who === "admin";
	return true;
}

export function showsUserSettingsTab(tab: string, who: Audience): boolean {
	return shown(tab, ADMIN_USER_TABS, HIDDEN_USER_TABS, who);
}

export function showsWorkspaceSettingsTab(tab: string, who: Audience): boolean {
	return shown(tab, OWNER_WORKSPACE_TABS, new Set(), who);
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
