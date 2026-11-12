import { authHeaders, BACKEND_URL } from "../helpers/api/auth";
import { listSources, type SourceDetail } from "../helpers/api/notebooks";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, openNotebook, test } from "./fixtures";

/**
 * "Read a web page" in the research box: paste a link and it is added as a
 * source, read in the background by the API's own page reader
 * (soloclef_assistant/pagereader). There is no Browserbase reader in this
 * tier, so the menu names none and the hint is the plain reader's.
 *
 * The route, the pending source and the sources list are real. The read
 * itself cannot succeed here: the stack has no route out, and the reader
 * will not connect to anything but a public address, so no stand-in page
 * can be put in front of it. What the spec can and does see is the other
 * half: the link is accepted at once, and the read that could not happen
 * ends with its reason written on the document, not as a row that spins
 * for ever.
 */

// TEST-NET-free on purpose: a documentation address is refused as "not a
// public web page", which is the next test's subject. This one is public.
const PAGE_URL = "https://93.184.215.14/e2e-page";
const SHOTS = process.env.E2E_SCREENSHOT_DIR;

test("a pasted page is taken as a source; a read that cannot happen ends, and says so", async ({
	page,
	request,
	token,
	notebook,
}, testInfo) => {
	test.setTimeout(150_000);
	await openNotebook(page, notebook.id, "Sources");

	await page.getByRole("button", { name: /^Search in:/ }).click();
	const item = page.getByRole("menuitemradio", { name: /Read a web page/ });
	await expect(item).toBeVisible();
	// Nothing but the plain reader here, so the menu names no other.
	await expect(item).not.toContainText("Browserbase");
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
	await expect(page.getByText(/read the same way as search results you add/)).toBeVisible();
	await expect(box).toHaveValue("");
	await expectNoOverflow(page, "read a web page: added");

	// Accepted at once: the notebook has the source before anything is read.
	await expect
		.poll(async () => (await listSources(request, token, notebook.id)).length, {
			timeout: 15_000,
		})
		.toBe(1);
	// The read has nowhere to go from this stack, and ends: the document
	// says it was not fetched and why, and is never left waiting. (The
	// source stays in the notebook, holding its address.)
	const [source] = (await listSources(request, token, notebook.id)) as SourceDetail[];
	await expect
		.poll(
			async () => {
				const response = await request.get(`${BACKEND_URL}/api/v1/documents/${source.target_id}`, {
					headers: authHeaders(token),
				});
				const document = (await response.json()) as {
					document_metadata?: { fetched?: boolean; fetch_problem?: string };
				};
				return document.document_metadata;
			},
			{ timeout: 120_000, intervals: [2_000] }
		)
		.toMatchObject({ fetched: false, fetch_problem: "The site could not be reached." });
	if (SHOTS && testInfo.project.name === "desktop") {
		await page.screenshot({ path: `${SHOTS}/02-page-read-failed.png` });
	}
});

test("a private address is refused inline", async ({ page, notebook }) => {
	await openNotebook(page, notebook.id, "Sources");
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
