import type { APIRequestContext, Page } from "@playwright/test";
import { BACKEND_URL, mintTestToken } from "./auth";

/**
 * A new account for one test, and the page signed in as it.
 *
 * Every other spec signs in as the one seeded e2e user, and the desktop and
 * phone projects run at the same time. That is fine for rows a test makes
 * and names itself (a notebook, a note), but not for what an account has
 * only one of: its Google Drive connection, its Nextcloud connection. Two
 * tests connecting and disconnecting the one account's Drive at once each
 * see the other's half-done state, and fail at "Connect" for a reason
 * that has nothing to do with the code under test. A test that owns such a
 * thing therefore gets an account of its own, made through the same
 * registration route a person uses, and nothing it does can meet another.
 *
 * The account is left in the stack's throwaway database; `down` deletes it.
 */

const SESSION_COOKIE_NAME = process.env.SESSION_COOKIE_NAME || "surfsense_session";
const PASSWORD = "E2eOwnAccount123!";

export type OwnAccount = { email: string; token: string };

export async function signInAsNewAccount(
	page: Page,
	request: APIRequestContext,
	label: string
): Promise<OwnAccount> {
	const email = `e2e-${label}-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 7)}@example.test`;
	// A spec's `request` carries the shared user's cookie, and the API's
	// CSRF check wants a write with a cookie to name its origin, as a
	// browser's registration form does.
	const registered = await request.post(`${BACKEND_URL}/auth/register`, {
		data: { email, password: PASSWORD },
		headers: { "Content-Type": "application/json", Origin: new URL(BACKEND_URL).origin },
	});
	if (registered.status() !== 201) {
		throw new Error(
			`registering ${email} failed (${registered.status()}): ${await registered.text()}`
		);
	}
	const token = await mintTestToken(request, email);

	// Out with the shared user's session, in with this one's, as
	// tests/auth.setup.ts sets it.
	const base = new URL(BACKEND_URL).origin;
	await page.context().clearCookies();
	await page
		.context()
		.addCookies([
			{ name: SESSION_COOKIE_NAME, value: token, url: base, httpOnly: true, sameSite: "Lax" },
		]);
	// The onboarding tour is keyed by the account; seen already, so it never
	// covers what the test clicks.
	const userId = JSON.parse(Buffer.from(token.split(".")[1] ?? "", "base64").toString("utf8")).sub;
	await page.addInitScript((uid: string) => {
		try {
			localStorage.setItem(`surfsense-tour-${uid}`, "true");
		} catch {
			// A page with no storage shows the tour; the click then fails loudly.
		}
	}, String(userId));
	return { email, token };
}
