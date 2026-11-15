import { getLoginPath, logout } from "@/lib/auth-utils";
import { resetUser, trackLogout } from "@/lib/posthog/events";

/**
 * Finish signing this browser out and open the sign-in page, as the account
 * menu's Logout does. `logout` ends this browser's sign-in (a no-op when
 * Sign out everywhere has already ended it and cleared the cookies).
 *
 * A full page load rather than a route change: nothing cached for the
 * account (lists, the profile, open chats) may outlive its sign-in.
 */
export async function leaveToSignIn(): Promise<void> {
	trackLogout();
	resetUser();
	await logout();
	window.location.href = getLoginPath();
}
