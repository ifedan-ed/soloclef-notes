import type { Page } from "@playwright/test";
import { expectHeaderClearsSafeArea, expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test } from "./fixtures";

/**
 * The public site as a signed-out visitor sees it, plus the two places a
 * signed-in person meets the brand: "/" sending them on to the dashboard, and
 * the app shell.
 *
 * Runs in both projects (desktop 1280 wide, phone 390 wide).
 */

const BRAND = "Soloclef Notes";
// The e2e stack's own address and contact (docker/docker-compose.e2e-web.yml):
// example values, as a real server sets them in docker/.env.
const SITE = "https://notes.example.test";
const SUPPORT_EMAIL = "help@notes.example.test";
const UPSTREAM_NAME = /surf\s*sense/i;

/** Pages that described the upstream product and are gone. */
const REMOVED = [
	"/downloads",
	"/license",
	"/license/activate",
	"/plugins",
	"/free",
	"/free/gpt-4o",
	"/sunset",
	"/mcp-server",
	"/external-mcp-connectors",
	"/private-ai-for-business",
	"/blog",
	"/changelog",
	"/announcements",
	"/reddit",
	"/youtube",
];

async function visibleText(page: Page): Promise<string> {
	return page.evaluate(() => document.body.innerText);
}

async function expectNoUpstreamName(page: Page, label: string) {
	expect(await page.title(), `${label}: title`).not.toMatch(UPSTREAM_NAME);
	expect(await visibleText(page), `${label}: visible text`).not.toMatch(UPSTREAM_NAME);
	const alts = await page
		.locator("img[alt]")
		.evaluateAll((els) => els.map((el) => el.getAttribute("alt") ?? ""));
	expect(alts.join(" | "), `${label}: image alt text`).not.toMatch(UPSTREAM_NAME);
}

test.describe("public site, signed out", () => {
	test.use({ storageState: { cookies: [], origins: [] } });

	test("landing: the pitch, Sign in, Request access, no upstream name, fits the screen", async ({
		page,
	}) => {
		const res = await page.goto("/");
		expect(res?.status()).toBe(200);
		await expect(page).toHaveURL(/\/$/);
		await expect(page).toHaveTitle(new RegExp(BRAND));
		await expect(
			page.getByRole("heading", { level: 1, name: /Notebooks that answer from your sources/ })
		).toBeVisible();

		const nav = page.locator("header.ss-home-nav");
		await expect(nav.getByRole("link", { name: "Sign in" })).toBeVisible();
		await expect(nav.getByText(BRAND)).toBeVisible();
		await expectHeaderClearsSafeArea(nav);

		// Sign-ups are closed: no Sign up call to action, a Request access link instead.
		await expect(
			page.getByRole("link", { name: /sign up|get started|create account/i })
		).toHaveCount(0);
		await expect(
			page.getByTestId("signup-note").getByRole("link", { name: "Request access" })
		).toHaveAttribute("href", "/contact");

		// The advantages, each from the real app.
		await expect(page.getByTestId("advantage")).toHaveCount(6);
		for (const claim of [
			"Answers from your sources, with citations",
			"Research that lands in the notebook",
			"Local or Cloud parsing, per upload",
			"Your files from Nextcloud",
		]) {
			await expect(page.getByRole("heading", { name: claim })).toBeVisible();
		}
		await expect(page.getByText("Where your documents live")).toBeVisible();

		// Nothing left over from the upstream marketing.
		const html = await page.content();
		expect(html).not.toMatch(
			/github\.com\/MODSetter|discord\.gg|reddit\.com\/r\/SurfSense|surfsense\.com/
		);
		await expect(page.locator('img[src*="/logos/"]')).toHaveCount(0);
		await expectNoUpstreamName(page, "landing");
		await expectNoOverflow(page, "landing");
	});

	test("landing metadata points at this site", async ({ page }) => {
		await page.goto("/");
		await expect(page.locator('link[rel="canonical"]')).toHaveAttribute(
			"href",
			new RegExp(`^${SITE.replace(/\./g, "\\.")}/?$`)
		);
		await expect(page.locator('meta[property="og:site_name"]')).toHaveAttribute("content", BRAND);
		await expect(page.locator('meta[property="og:image"]').first()).toHaveAttribute(
			"content",
			/og-image\.png/
		);
		await expect(page.locator('link[rel="manifest"]')).toHaveCount(1);
	});

	test("features page", async ({ page }) => {
		const res = await page.goto("/features");
		expect(res?.status()).toBe(200);
		await expect(
			page.getByRole("heading", { level: 1, name: "Everything a notebook can do" })
		).toBeVisible();
		for (const heading of [
			"Bring in what you are reading",
			"Ask, and see where the answer came from",
			"Make something from your sources",
			"Notes, search and tidy notebooks",
		]) {
			await expect(page.getByRole("heading", { level: 2, name: heading })).toBeVisible();
		}
		await expect(page.getByText(/PubMed, arXiv, medRxiv and bioRxiv/).first()).toBeVisible();
		await expectNoUpstreamName(page, "features");
		await expectNoOverflow(page, "features");
	});

	test("pricing: three plans, top-ups and credit costs; buying says Coming soon", async ({
		page,
	}) => {
		const res = await page.goto("/pricing");
		expect(res?.status()).toBe(200);
		await expect(page).toHaveTitle(new RegExp(`Pricing · ${BRAND}`));

		const plan = (slug: string) => page.getByTestId(`plan-${slug}`);
		await expect(plan("free")).toContainText("$0");
		await expect(plan("free")).toContainText("100 credits a month");
		await expect(plan("plus")).toContainText("$9");
		await expect(plan("plus")).toContainText("1,500 credits a month");
		await expect(plan("pro")).toContainText("$24");
		await expect(plan("pro")).toContainText("5,000 credits a month");
		await expect(page.getByTestId("pack-credits-500")).toContainText("$5");

		// Billing is in shadow mode: nothing claims payments work.
		await expect(page.getByTestId("billing-not-live")).toBeVisible();
		const comingSoon = page.getByTestId("buy-coming-soon");
		await expect(comingSoon.first()).toBeVisible();
		expect(await comingSoon.count()).toBeGreaterThanOrEqual(3);
		for (const button of await comingSoon.all()) {
			await expect(button).toHaveAttribute("aria-disabled", "true");
		}
		await expect(page.getByRole("link", { name: /^(Choose|Buy)\b/ })).toHaveCount(0);
		// No method is set up on this stack, so none is promised (Stripe is pending).
		await expect(page.getByText(/\bby card\b|stripe/i)).toHaveCount(0);
		await expect(page.getByText(/Payments are not open yet/)).toBeAttached();

		const costs = page.getByTestId("credit-costs");
		await expect(costs.getByRole("row", { name: /Video overview/ })).toContainText("40");
		await expect(costs.getByRole("row", { name: /Local parsing/ })).toContainText("Free");

		await expectNoUpstreamName(page, "pricing");
		await expectNoOverflow(page, "pricing");
	});

	test("docs open signed out and carry the brand", async ({ page }) => {
		const res = await page.goto("/docs");
		expect(res?.status()).toBe(200);
		await expect(page.getByRole("heading", { level: 1 }).first()).toBeVisible();
		expect(await visibleText(page)).toContain(BRAND);
		await expectNoUpstreamName(page, "docs");
		await expectNoOverflow(page, "docs");

		for (const slug of ["getting-started", "studio", "plans-and-credits"]) {
			const doc = await page.goto(`/docs/${slug}`);
			expect(doc?.status(), `/docs/${slug}`).toBe(200);
			await expectNoUpstreamName(page, `/docs/${slug}`);
		}
	});

	test("contact shows the address as text with a working copy button", async ({
		page,
		context,
		browserName,
	}) => {
		if (browserName === "chromium") {
			await context.grantPermissions(["clipboard-read", "clipboard-write"]);
		}
		await page.goto("/contact");
		await expect(page.getByTestId("contact-email")).toHaveText(SUPPORT_EMAIL);
		await page.getByTestId("copy-email").click();
		await expect(page.getByTestId("copy-email")).toContainText("Copied");
		if (browserName === "chromium") {
			expect(await page.evaluate(() => navigator.clipboard.readText())).toBe(SUPPORT_EMAIL);
		}
		await expectNoOverflow(page, "contact");
	});

	test("privacy and terms", async ({ page }) => {
		for (const [path, heading] of [
			["/privacy", "Privacy"],
			["/terms", "Terms"],
		] as const) {
			const res = await page.goto(path);
			expect(res?.status(), path).toBe(200);
			await expect(page.getByRole("heading", { level: 1, name: heading })).toBeVisible();
			await expectNoUpstreamName(page, path);
			await expectNoOverflow(page, path);
		}
		await page.goto("/privacy");
		await expect(page.getByText("Datalab").first()).toBeVisible();
		await expect(page.getByText("LlamaCloud").first()).toBeVisible();
		await expect(page.getByText("BGE-M3").first()).toBeVisible();
	});

	test("upstream-only pages are gone", async ({ request }) => {
		for (const path of REMOVED) {
			const res = await request.get(path, { maxRedirects: 0 });
			expect(res.status(), path).toBe(404);
		}
	});

	test("sign-in offers Request access; /register says sign-ups are closed", async ({ page }) => {
		await page.goto("/login");
		await expect(page.getByRole("link", { name: "Request access" })).toHaveAttribute(
			"href",
			"/contact"
		);
		await expect(page.getByRole("button", { name: /sign in with|continue with/i })).toHaveCount(0);
		await expectNoUpstreamName(page, "login");

		await page.goto("/register");
		await expect(
			page.getByRole("heading", { name: "New accounts are not open yet" })
		).toBeVisible();
		await expect(page.getByLabel(/password/i)).toHaveCount(0);
		await expectNoUpstreamName(page, "register");
		await expectNoOverflow(page, "register (closed)");
	});

	test("sitemap, robots and manifest describe this server", async ({ request }) => {
		const sitemap = await (await request.get("/sitemap.xml")).text();
		expect(sitemap).toContain(`${SITE}/pricing`);
		expect(sitemap).toContain(`${SITE}/docs`);
		expect(sitemap).toContain(`${SITE}/docs/self-hosting`);
		expect(sitemap).not.toMatch(/surfsense\.com|\/blog|\/changelog|\/downloads|\/free/);

		const robots = await (await request.get("/robots.txt")).text();
		expect(robots).toContain(`Sitemap: ${SITE}/sitemap.xml`);
		expect(robots).toContain("Disallow: /dashboard/");

		const manifest = await (await request.get("/manifest.webmanifest")).json();
		expect(manifest.name).toBe(BRAND);
		expect(manifest.icons.length).toBeGreaterThanOrEqual(2);
		for (const icon of manifest.icons as { src: string }[]) {
			expect((await request.get(icon.src)).status(), icon.src).toBe(200);
		}
	});
});

test.describe("signed in", () => {
	test("/ goes straight to the notebooks", async ({ page }) => {
		await page.goto("/");
		await expect(page).toHaveURL(/\/dashboard\/\d+\/notebooks$/);
	});

	test("the app shell shows the brand, not the upstream name", async ({ page, workspace }) => {
		await page.goto(`/dashboard/${workspace.id}/notebooks`);
		await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
		await expect(page).toHaveTitle(new RegExp(BRAND));
		await expectNoUpstreamName(page, "notebooks home");
		await expect(page.locator(`svg[aria-label="${BRAND}"]`).first()).toBeVisible();
	});
});
