import { listSources } from "../helpers/api/notebooks";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, openNotebook, test } from "./fixtures";

/**
 * "Read a web page" in the research box: paste a link and it is added as a
 * source. The e2e worker's Browserbase is the stand-in in
 * surfsense_backend/tests/e2e/fakes/browserbase.py (its REST API faked at the
 * HTTP boundary, its browser rendering a fixed page), and every workspace
 * counts as having Browserbase connected. The route, the pending source, the
 * background read, indexing and the sources list are all real.
 */

const PAGE_URL = "https://93.184.215.14/e2e-js-page";
const PAGE_TITLE = "E2E rendered page";
const SHOTS = process.env.E2E_SCREENSHOT_DIR;

test("a pasted page is read with Browserbase and becomes a source", async ({
	page,
	request,
	token,
	workspace,
	notebook,
}, testInfo) => {
	test.setTimeout(120_000);
	await openNotebook(page, workspace.id, notebook.id, "Sources");

	await page.getByRole("button", { name: /^Search in:/ }).click();
	const item = page.getByRole("menuitemradio", { name: /Read a web page/ });
	await expect(item).toBeVisible();
	// Browserbase is connected here, so the menu says what reads the page.
	await expect(item).toContainText("Browserbase");
	await expectNoOverflow(page, "research menu with Read a web page");
	if (SHOTS && testInfo.project.name === "desktop") {
		await page.screenshot({ path: `${SHOTS}/00-research-menu-read-a-web-page.png` });
	}
	await item.click();

	const box = page.getByRole("textbox", { name: "Paste a link to a web page" });
	await expect(box).toBeVisible();
	// No research mode for a single page, and no submit until it is a link.
	await expect(page.getByRole("button", { name: /^Research mode:/ })).toHaveCount(0);
	await box.fill("not a link");
	await expect(page.getByRole("button", { name: "Add page" })).toBeDisabled();
	await box.fill(PAGE_URL);
	if (SHOTS && testInfo.project.name === "desktop") {
		await page.screenshot({ path: `${SHOTS}/01-read-a-web-page-box.png` });
	}
	await page.getByRole("button", { name: "Add page" }).click();

	await expect(page.getByText("Page added.", { exact: false })).toBeVisible();
	await expect(page.getByText(/Browserbase opens it in a real browser/)).toBeVisible();
	await expect(box).toHaveValue("");
	await expectNoOverflow(page, "read a web page: added");

	// Read in the background: the pending row takes the page's own title.
	await expect
		.poll(
			async () =>
				(await listSources(request, token, workspace.id, notebook.id)).map((s) => s.title),
			{ timeout: 60_000, intervals: [1_000] }
		)
		.toContain(PAGE_TITLE);
	await expect(page.getByText(PAGE_TITLE).first()).toBeVisible({ timeout: 30_000 });
	if (SHOTS && testInfo.project.name === "desktop") {
		await page.screenshot({ path: `${SHOTS}/02-page-added-as-source.png` });
	}
});

test("a private address is refused inline", async ({ page, workspace, notebook }) => {
	await openNotebook(page, workspace.id, notebook.id, "Sources");
	await page.getByRole("button", { name: /^Search in:/ }).click();
	await page.getByRole("menuitemradio", { name: /Read a web page/ }).click();
	await page
		.getByRole("textbox", { name: "Paste a link to a web page" })
		.fill("http://169.254.169.254/latest/meta-data/");
	await page.getByRole("button", { name: "Add page" }).click();
	const alert = page.getByRole("alert").filter({ hasText: "Couldn't add that page." });
	await expect(alert).toBeVisible();
	await expect(alert).toContainText("isn't a public web page");
	await expectNoOverflow(page, "read a web page: refused");
	await alert.getByRole("button", { name: "Dismiss" }).click();
	await expect(alert).toBeHidden();
});
