import type { APIRequestContext, Browser, Page } from "@playwright/test";
import { authHeaders, BACKEND_URL } from "../helpers/api/auth";
import { seedOutput } from "../helpers/api/notebooks";
import { expect, openNotebook, test, uniqueName } from "./fixtures";

/**
 * Public links, end to end: the owner shares a Studio output through the share
 * dialog (warning shown, 7 days by default), a signed-out visitor reads it,
 * the owner revokes it from Settings › Public links and the link is
 * gone; a second link expired through the test-only route says it expired.
 * The notebook menu's "Share chat publicly…" refuses an empty chat inline.
 */

const MINT_SECRET = process.env.E2E_MINT_SECRET ?? "";

async function visitorPage(browser: Browser): Promise<Page> {
	const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	return context.newPage();
}

async function expireLink(request: APIRequestContext, snapshotId: number) {
	const response = await request.post(`${BACKEND_URL}/__e2e__/public-links/${snapshotId}/expire`, {
		// Same-origin, as the page's own requests are (the context carries the session cookie).
		headers: { "X-E2E-Mint-Secret": MINT_SECRET, Origin: new URL(BACKEND_URL).origin },
	});
	expect(response.ok(), await response.text()).toBeTruthy();
}

test("share an output, read it signed out, revoke it, and see an expired link", async ({
	page,
	browser,
	request,
	token,
	notebook,
}) => {
	// Unique per run: Settings › Public links lists every link of the account,
	// the other project's copy of this test included.
	const TITLE = uniqueName("E2E shared report");
	const seeded = await seedOutput(request, notebook.id, "report", "ready", TITLE);

	await openNotebook(page, notebook.id, "Studio");
	const row = page.getByRole("button", { name: new RegExp(`^${TITLE}`) }).first();
	await expect(row).toBeEnabled({ timeout: 30_000 });
	await row.click();
	await page.getByTestId("output-share").click();

	const dialog = page.getByTestId("share-dialog");
	await expect(dialog.getByText("Anyone with the link can view this.")).toBeVisible();
	await expect(dialog.getByText(/patient-identifiable/)).toHaveCount(0);
	await expect(dialog.getByRole("combobox")).toHaveText(/After 7 days/);
	await dialog.getByRole("button", { name: "Create link" }).click();
	const linkBox = dialog.getByRole("textbox", { name: "Public link" });
	await expect(linkBox).toHaveValue(/\/public\//);
	const url = await linkBox.inputValue();
	await expect(dialog.getByText(/Shared read-only · expires/)).toBeVisible();
	await dialog.getByRole("button", { name: "Done" }).click();

	const visitor = await visitorPage(browser);
	await visitor.goto(url);
	await expect(visitor.getByRole("heading", { name: TITLE }).first()).toBeVisible({
		timeout: 30_000,
	});
	await expect(visitor.getByTestId("shared-notice")).toContainText("Shared read-only");

	await page.goto(`/dashboard/settings/public-links`);
	const linkRow = page.getByTestId("public-link-row").filter({ hasText: TITLE });
	await expect(linkRow).toContainText(/Expires/);
	await linkRow.getByRole("button", { name: "Revoke" }).click();
	await page.getByRole("alertdialog").getByRole("button", { name: "Revoke" }).click();
	await expect(linkRow).toHaveCount(0);

	await visitor.reload();
	await expect(
		visitor.getByRole("heading", { name: "This link is no longer available" })
	).toBeVisible();

	// A second link, expired through the test-only route.
	const created = await request.post(
		`${BACKEND_URL}/api/v1/notebooks/${notebook.id}/studio/outputs/${seeded.output_id}/public-link`,
		{ headers: authHeaders(token), data: { expires_in_days: 30 } }
	);
	expect(created.ok(), await created.text()).toBeTruthy();
	const { snapshot_id, share_token } = (await created.json()) as {
		snapshot_id: number;
		share_token: string;
	};
	await expireLink(request, snapshot_id);
	await visitor.goto(`/public/${share_token}`);
	await expect(visitor.getByRole("heading", { name: "This link has expired" })).toBeVisible();
	await visitor.context().close();
});

test("an empty notebook chat cannot be shared, and says why inline", async ({ page, notebook }) => {
	await openNotebook(page, notebook.id, "Chat");
	await page.getByRole("button", { name: "Notebook options" }).click();
	await page.getByRole("menuitem", { name: "Share chat publicly…" }).click();
	const dialog = page.getByTestId("share-dialog");
	await dialog.getByRole("button", { name: "Create link" }).click();
	await expect(dialog.getByRole("alert")).toHaveText(/no messages yet/);
});
