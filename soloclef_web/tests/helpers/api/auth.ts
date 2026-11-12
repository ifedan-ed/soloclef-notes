import type { APIRequestContext } from "@playwright/test";

/**
 * Direct backend auth helper. Uses the desktop login endpoint when the
 * rate-limit-free e2e mint endpoint is unavailable.
 *
 * Returns a bearer token specs can attach to API calls when they don't
 * want to go through the browser. The browser-side auth (cookie storage)
 * is set up separately by tests/auth.setup.ts.
 */

export const BACKEND_URL = process.env.NEXT_PUBLIC_FASTAPI_BACKEND_URL || "http://localhost:8000";

const TEST_USER_EMAIL = process.env.PLAYWRIGHT_TEST_EMAIL || "e2e-test@soloclef.net";
const TEST_USER_PASSWORD = process.env.PLAYWRIGHT_TEST_PASSWORD || "E2eTestPassword123!";
const E2E_MINT_SECRET = process.env.E2E_MINT_SECRET || "local-e2e-mint-secret-not-for-production";

/**
 * Mints a session token for the seeded e2e user through the API's test-only
 * route, which the e2e overlay switches on (SOLOCLEF_API_E2E_ROUTES in
 * docker/docker-compose.e2e-web-go.yml). It bypasses the sign-in rate limit,
 * so it is safe to call from any worker or retry. A server without the route
 * answers 404 (someone is pointing the suite at a server that is not a test
 * stack).
 */
export async function mintTestToken(
	request: APIRequestContext,
	email: string = TEST_USER_EMAIL
): Promise<string> {
	const response = await request.post(`${BACKEND_URL}/__e2e__/auth/token`, {
		data: { email },
		headers: {
			"Content-Type": "application/json",
			"X-E2E-Mint-Secret": E2E_MINT_SECRET,
			// A spec's `request` carries the signed-in cookie, and a write
			// with a cookie must name its origin (the API's CSRF check).
			Origin: new URL(BACKEND_URL).origin,
		},
	});
	if (!response.ok()) {
		throw new Error(
			`Mint token at ${BACKEND_URL}/__e2e__/auth/token failed (${response.status()}): ${await response.text()}`
		);
	}
	const { access_token } = (await response.json()) as { access_token: string };
	if (!access_token) {
		throw new Error("Mint response missing access_token");
	}
	return access_token;
}

export async function loginAsTestUser(request: APIRequestContext): Promise<string> {
	const response = await request.post(`${BACKEND_URL}/auth/desktop/login`, {
		data: {
			email: TEST_USER_EMAIL,
			password: TEST_USER_PASSWORD,
		},
		headers: { "Content-Type": "application/json" },
	});

	if (!response.ok()) {
		throw new Error(
			`Login to ${BACKEND_URL}/auth/desktop/login failed (${response.status()}): ${await response.text()}`
		);
	}

	const { access_token } = (await response.json()) as { access_token: string };
	if (!access_token) {
		throw new Error("Backend response missing access_token");
	}
	return access_token;
}

/**
 * Get a bearer token by trying the rate-limit-free mint endpoint first
 * and falling back to /auth/desktop/login if the e2e endpoint isn't mounted
 * (e.g. running against a non-e2e backend in local dev).
 */
export async function acquireTestToken(request: APIRequestContext): Promise<string> {
	try {
		return await mintTestToken(request);
	} catch (err) {
		const msg = err instanceof Error ? err.message : String(err);
		if (msg.includes("(404)") || msg.includes("(405)")) {
			return loginAsTestUser(request);
		}
		throw err;
	}
}

/**
 * Standard auth headers for API calls, with any extra headers a test adds.
 */
export function authHeaders(token: string, extra?: Record<string, string>): Record<string, string> {
	return {
		Authorization: `Bearer ${token}`,
		"Content-Type": "application/json",
		...(extra ?? {}),
	};
}
