import { defineConfig } from "@playwright/test";
import { desktopDevice as desktop, phoneDevice as phone } from "./tests/helpers/devices";

/**
 * Playwright configuration for the SurfSense web E2E suite.
 *
 * Two targets, never mixed:
 *
 * 1. The isolated stack (default). `scripts/e2e-stack.sh up` starts a
 *    throwaway copy of the whole app — own Postgres (tmpfs), Redis, the E2E
 *    backend with fake LLMs and the /__e2e__ token mint, Celery, zero-cache,
 *    this checkout's frontend and the same Caddy routing — on
 *    http://127.0.0.1:3940. Projects `setup`, `desktop` and `phone` run here
 *    and may create, edit and delete anything.
 *
 * 2. Production, read-only. Project `prod-smoke` runs tests/prod-smoke against
 *    PROD_BASE_URL (default https://notes.example.com) with a token supplied
 *    in PROD_E2E_TOKEN. Every non-GET API request is aborted by a route guard
 *    (tests/prod-smoke/guard.ts) except an explicit list of read-only POSTs.
 *
 * Tests and this file are never bundled into the production image (see
 * .dockerignore); @playwright/test is a devDependency.
 */

const E2E_PORT = process.env.E2E_WEB_PORT || "3940";
const baseURL = process.env.PLAYWRIGHT_BASE_URL || `http://127.0.0.1:${E2E_PORT}`;
const prodBaseURL = process.env.PROD_BASE_URL || "https://notes.example.com";

process.env.PLAYWRIGHT_TEST_EMAIL ??= "e2e-test@surfsense.net";
process.env.PLAYWRIGHT_TEST_PASSWORD ??= "E2eTestPassword123!";
// The isolated stack is same-origin behind Caddy: /api/v1, /auth, /__e2e__
// all reach the backend through the base URL.
process.env.NEXT_PUBLIC_FASTAPI_BACKEND_URL ??= baseURL;
process.env.E2E_MINT_SECRET ??= "e2e-mint-secret-not-for-production";
process.env.PLAYWRIGHT_BASE_URL ??= baseURL;

export default defineConfig({
	testDir: "./tests",
	// Node unit tests live under tests/unit; Playwright only runs *.spec.ts journeys.
	testIgnore: ["**/unit/**"],
	timeout: 60_000,
	expect: { timeout: 15_000 },
	fullyParallel: true,
	forbidOnly: !!process.env.CI,
	retries: process.env.CI ? 1 : 0,
	workers: Number(process.env.E2E_WORKERS || 3),
	reporter: process.env.CI
		? [["html", { open: "never" }], ["github"], ["list"]]
		: [["html", { open: "never" }], ["list"]],
	use: {
		baseURL,
		trace: "retain-on-failure",
		screenshot: "only-on-failure",
		video: "off",
		extraHTTPHeaders: { "x-playwright-test": "true" },
	},
	// `pnpm test:e2e` selects desktop + phone (setup runs as their
	// dependency); `pnpm test:e2e:prod-smoke` selects prod-smoke alone.
	projects: [
		{
			name: "setup",
			testMatch: /auth\.setup\.ts/,
			use: { ...desktop },
		},
		{
			name: "desktop",
			testDir: "./tests/app",
			dependencies: ["setup"],
			use: { ...desktop, storageState: "playwright/.auth/user.json" },
		},
		{
			name: "phone",
			testDir: "./tests/app",
			dependencies: ["setup"],
			use: { ...phone, storageState: "playwright/.auth/user.json" },
		},
		{
			// Viewports are set per describe block inside the specs.
			name: "prod-smoke",
			testDir: "./tests/prod-smoke",
			fullyParallel: false,
			use: { baseURL: prodBaseURL, extraHTTPHeaders: {} },
		},
	],
});
