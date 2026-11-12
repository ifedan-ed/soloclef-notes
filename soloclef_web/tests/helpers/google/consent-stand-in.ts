import type { Page } from "@playwright/test";

/**
 * Google's consent page, faked at the browser boundary, for a stack whose
 * API sends the browser to Google's real address.
 *
 * The Go API does: accounts.google.com is a constant in its code, not a
 * setting, so that no deployment can be configured to send a person's grant
 * somewhere else. A test therefore cannot hand it the stand-in's address the
 * way the Python stack's settings did. What it can do is own the browser:
 * the navigation to Google's consent address is answered here with a
 * redirect to the stand-in's consent page (/__google__, through the stack's
 * proxy), carrying the query the API wrote, unchanged. Everything the API
 * put in that query (client, scope, state, PKCE challenge, redirect address)
 * is still what the stand-in checks and what the spec asserts on.
 *
 * The server side needs no such trick: in that stack the API and the worker
 * reach the stand-in under Google's own API names (docker-compose.e2e-web-go.yml).
 *
 * Harmless on a stack that already sends the browser to the stand-in: the
 * route never matches.
 */
export async function sendGoogleConsentToStandIn(page: Page): Promise<void> {
	await page.route("https://accounts.google.com/o/oauth2/auth?*", async (route) => {
		const asked = new URL(route.request().url());
		const base = new URL(page.url()).origin;
		await route.fulfill({
			status: 302,
			headers: { location: `${base}/__google__/o/oauth2/auth${asked.search}` },
		});
	});
}
