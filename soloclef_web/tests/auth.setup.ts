import path from "node:path";
import { expect, test as setup } from "@playwright/test";
import { acquireTestToken } from "./helpers/api/auth";

/**
 * One-time authentication for the isolated stack. Mints a token for the
 * seeded e2e user (rate-limit-free /__e2e__/auth/token, desktop-login
 * fallback) and stores it as the `surfsense_session` cookie, so every test in
 * the desktop and phone projects starts signed in.
 *
 * Also pre-seeds the localStorage flag behind the onboarding tour
 * (`surfsense-tour-<userId>`) so it never intercepts clicks.
 */

const authFile = path.join(__dirname, "..", "playwright", ".auth", "user.json");
const SESSION_COOKIE_NAME = process.env.SESSION_COOKIE_NAME || "surfsense_session";

/** Decode the user id (`sub`) from a JWT without verifying the signature. */
export function decodeUserId(token: string): string | null {
	try {
		const payload = token.split(".")[1];
		if (!payload) return null;
		const obj = JSON.parse(Buffer.from(payload, "base64").toString("utf8")) as { sub?: string };
		return obj.sub ?? null;
	} catch {
		return null;
	}
}

setup("authenticate", async ({ page, request, baseURL }) => {
	const access_token = await acquireTestToken(request);
	expect(access_token, "Failed to acquire e2e bearer token").toBeTruthy();

	const userId = decodeUserId(access_token);

	await page.context().addCookies([
		{
			name: SESSION_COOKIE_NAME,
			value: access_token,
			url: baseURL ?? "http://127.0.0.1:3940",
			httpOnly: true,
			sameSite: "Lax",
		},
	]);

	// Use a public page so the init script can write localStorage without
	// racing the dashboard auth redirect.
	await page.goto("/login", { waitUntil: "domcontentloaded" });
	await page.evaluate(
		({ uid }) => {
			if (uid) localStorage.setItem(`surfsense-tour-${uid}`, "true");
		},
		{ uid: userId }
	);

	await page.context().storageState({ path: authFile });
});
