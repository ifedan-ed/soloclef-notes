import type { APIRequestContext } from "@playwright/test";
import { authHeaders, BACKEND_URL } from "../helpers/api/auth";
import { createNotebook, deleteNotebook, seedOutput } from "../helpers/api/notebooks";
import { expect, test, uniqueName } from "./fixtures";

/**
 * The web app as static files behind the stack's Caddy (docker/proxy-go/
 * routes.caddy): what a Next.js server used to answer per request, checked
 * where it is answered now. Each test asks the site as a browser would and
 * reads the raw answer: its status, its headers, its <head>.
 */

/** A request with no session, whatever the project's storage state. */
async function signedOut(request: APIRequestContext, path: string) {
	return request.get(path, { headers: { Cookie: "" }, maxRedirects: 0 });
}

const SECURITY_HEADERS = {
	"content-security-policy": "frame-ancestors 'self'",
	"x-frame-options": "SAMEORIGIN",
	"x-content-type-options": "nosniff",
	"referrer-policy": "strict-origin-when-cross-origin",
};

test.describe("static site", () => {
	test("pages carry the security headers and are asked about every time", async ({ request }) => {
		for (const path of ["/", "/login", "/docs", "/contact", "/dashboard/notebooks"]) {
			const response = await signedOut(request, path);
			expect(response.status(), path).toBe(200);
			expect(response.headers()["content-type"], path).toMatch(/text\/html/);
			expect(response.headers()["cache-control"], path).toBe("no-cache");
			for (const [name, value] of Object.entries(SECURITY_HEADERS)) {
				expect(response.headers()[name], `${path} ${name}`).toBe(value);
			}
		}
	});

	test("hashed assets are kept a year; a missing path is the app's 404", async ({ request }) => {
		const html = await (await signedOut(request, "/login")).text();
		const asset = /\/_next\/static\/[^"']+\.js/.exec(html)?.[0];
		expect(asset, "the login page loads a script bundle").toBeTruthy();
		const script = await signedOut(request, asset as string);
		expect(script.status()).toBe(200);
		expect(script.headers()["cache-control"]).toBe("public, max-age=31536000, immutable");

		const missing = await signedOut(request, "/no-such-page");
		expect(missing.status()).toBe(404);
		expect(missing.headers()["content-type"]).toMatch(/text\/html/);
		expect(missing.headers()["x-frame-options"]).toBe("SAMEORIGIN");
	});

	test("Caddy answers the redirects Next.js used to", async ({ request }) => {
		const cases: [string, number, string][] = [
			["/dashboard", 302, "/dashboard/notebooks"],
			["/dashboard/settings", 302, "/dashboard/settings/profile"],
			["/dashboard/automations", 302, "/dashboard/settings/automations"],
			["/docs/legacy/installation", 308, "/docs"],
			["/docs/connectors", 308, "/docs"],
			["/docs/workspaces-and-sharing", 308, "/docs/account-and-security"],
			["/docs/account-and-sharing", 308, "/docs/account-and-security"],
		];
		for (const [from, status, to] of cases) {
			const response = await signedOut(request, from);
			expect(response.status(), from).toBe(status);
			expect(new URL(response.headers().location, BACKEND_URL).pathname, from).toBe(to);
		}
		// A visitor gets the landing page; a session cookie goes to the notebooks.
		expect((await signedOut(request, "/")).status()).toBe(200);
		const signedIn = await request.get("/", {
			headers: { Cookie: "surfsense_session=anything" },
			maxRedirects: 0,
		});
		expect(signedIn.status()).toBe(302);
		expect(signedIn.headers().location).toBe("/dashboard/notebooks");
	});

	test("the docs search index is built with the site", async ({ request }) => {
		const response = await signedOut(request, "/api/search");
		expect(response.status()).toBe(200);
		expect(response.headers()["content-type"]).toMatch(/application\/json/);
		expect(await response.json()).toBeTruthy();
	});

	test("the docs search finds a page in the browser", async ({ page, isPhone }) => {
		test.skip(isPhone, "the search box is the same on a phone; one run is enough");
		await page.goto("/docs");
		// The index is fetched once, when the search is first used.
		const search = page.waitForResponse((r) => new URL(r.url()).pathname === "/api/search");
		await page
			.locator("button", { hasText: /search/i })
			.first()
			.click();
		await page.getByPlaceholder("Search").fill("notebook");
		expect((await search).status()).toBe(200);
		await expect(page.getByRole("dialog").getByText("Notebooks and sources").first()).toBeVisible({
			timeout: 15_000,
		});
	});

	test("a deep link to a notebook or a note opens it, and so does a click to another", async ({
		page,
		request,
		token,
		notebook,
	}) => {
		// One file serves every notebook; the page reads the id from the address.
		await page.goto(`/dashboard/notebooks/${notebook.id}`);
		const title = page.getByRole("textbox", { name: "Notebook title" }).first();
		await expect(title).toHaveValue(notebook.title, { timeout: 30_000 });

		// Inside the app the move is a client navigation, which fetches the
		// page's data file for the new address: Caddy answers that too.
		const second = await createNotebook(request, token, uniqueName("E2E second notebook"));
		try {
			await page.goto("/dashboard/notebooks");
			await page.getByText(second.title).first().click();
			await expect(page).toHaveURL(new RegExp(`/dashboard/notebooks/${second.id}$`));
			await expect(title).toHaveValue(second.title, { timeout: 30_000 });
		} finally {
			await deleteNotebook(request, token, second.id).catch(() => undefined);
		}

		const note = await page.goto("/dashboard/notes/987654321");
		expect(note?.status()).toBe(200);
		await expect(page).toHaveURL(/\/dashboard\/notes\/987654321$/);
	});

	test("a public link's page names what it shares in its link preview", async ({
		request,
		token,
		notebook,
		browser,
	}) => {
		const title = uniqueName('E2E card <&> "quoted"');
		const seeded = await seedOutput(request, notebook.id, "report", "ready", title);
		const created = await request.post(
			`${BACKEND_URL}/api/v1/notebooks/${notebook.id}/studio/outputs/${seeded.output_id}/public-link`,
			{ headers: authHeaders(token), data: { expires_in_days: 7 } }
		);
		expect(created.ok(), await created.text()).toBeTruthy();
		const { share_token } = (await created.json()) as { share_token: string };

		// What a link-preview bot reads: the raw HTML, no script run.
		const response = await signedOut(request, `/public/${share_token}`);
		expect(response.status()).toBe(200);
		const html = await response.text();
		const escaped = title
			.replaceAll("&", "&amp;")
			.replaceAll("<", "&lt;")
			.replaceAll(">", "&gt;")
			.replaceAll('"', "&#34;");
		expect(html).toContain(`<title>${escaped}</title>`);
		expect(html).toContain(`<meta property="og:title" content="${escaped}"/>`);
		expect(html).toMatch(/<meta property="og:description" content="[^"]+ · shared read-only from /);
		expect(html).toContain('<meta property="og:type" content="article"/>');
		expect(html).toContain('<meta name="robots" content="noindex, nofollow, nocache"/>');
		expect(html).not.toContain("[[%");

		// An unknown token is the site's own card, still a page that opens.
		const unknown = await signedOut(request, "/public/no-such-token");
		expect(unknown.status()).toBe(200);
		expect(await unknown.text()).toContain('<meta property="og:type" content="website"/>');

		// And a visitor sees the item, with its title in the tab.
		const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
		const visitor = await context.newPage();
		await visitor.goto(`/public/${share_token}`);
		await expect(visitor.getByRole("heading", { name: title }).first()).toBeVisible({
			timeout: 30_000,
		});
		await expect(visitor).toHaveTitle(title);
		await context.close();
	});

	test("the contact page shows the API's address, read in the browser", async ({ page }) => {
		await page.goto("/contact");
		await expect(page.getByTestId("contact-email")).toHaveText("help@notes.example.test");
	});

	test("the sign-in page draws the password form, and no SSO button with SSO off", async ({
		browser,
	}) => {
		const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
		const page = await context.newPage();
		await page.goto("/login");
		await expect(page.getByLabel(/email/i).first()).toBeVisible();
		await expect(page.getByRole("button", { name: /single sign-on/i })).toHaveCount(0);
		await context.close();
	});
});
