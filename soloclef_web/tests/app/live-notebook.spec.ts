import fs from "node:fs";
import path from "node:path";
import type { APIRequestContext, Browser, Page } from "@playwright/test";
import { authHeaders, BACKEND_URL } from "../helpers/api/auth";
import { seedOutput } from "../helpers/api/notebooks";
import { assistantMessages, send } from "../helpers/ui/chat";
import { expect, openNotebook, test } from "./fixtures";

/**
 * A live notebook link, end to end: the Owner chats (a citation to a PDF
 * source), opens "Share notebook…" (warning, 7 days by default), and a
 * signed-out visitor sees the chat with its citation, the finished outputs
 * and the sources; opens an output and the cited PDF; then a new output
 * appears on the visitor's page without sharing again. Expiry shows the
 * expired page; a new link revoked from Workspace settings › Public links is
 * gone, and "Stop sharing" from the Shared badge ends the next one.
 */

const MINT_SECRET = process.env.E2E_MINT_SECRET ?? "";
const SHOTS = process.env.LIVE_SHARE_SHOTS ?? "";

function seedHeaders() {
	return {
		"Content-Type": "application/json",
		"X-E2E-Mint-Secret": MINT_SECRET,
		Origin: process.env.PLAYWRIGHT_BASE_URL ?? BACKEND_URL,
	};
}

async function seedPdfSource(request: APIRequestContext, ws: number, nb: number, title: string) {
	const response = await request.post(`${BACKEND_URL}/__e2e__/seed/source-pdf`, {
		headers: seedHeaders(),
		data: { workspace_id: ws, notebook_id: nb, title },
	});
	expect(response.ok(), await response.text()).toBeTruthy();
	return (await response.json()) as { document_id: number; chunk_id: number };
}

async function expireLink(request: APIRequestContext, linkId: number) {
	const response = await request.post(
		`${BACKEND_URL}/__e2e__/public-links/notebook/${linkId}/expire`,
		{ headers: seedHeaders() }
	);
	expect(response.ok(), await response.text()).toBeTruthy();
}

async function linkState(request: APIRequestContext, token: string, ws: number, nb: number) {
	const response = await request.get(
		`${BACKEND_URL}/api/v1/workspaces/${ws}/notebooks/${nb}/public-link`,
		{ headers: authHeaders(token) }
	);
	expect(response.ok(), await response.text()).toBeTruthy();
	return ((await response.json()) as { link: { id: number; share_token: string } | null }).link;
}

async function visitorPage(
	browser: Browser,
	like: Page
): Promise<{ page: Page; errors: string[] }> {
	const viewport = like.viewportSize() ?? { width: 1280, height: 800 };
	const phone = viewport.width < 640;
	const context = await browser.newContext({
		storageState: { cookies: [], origins: [] },
		viewport,
		isMobile: phone,
		hasTouch: phone,
	});
	const page = await context.newPage();
	const errors: string[] = [];
	page.on("pageerror", (error) => errors.push(error.message));
	return { page, errors };
}

async function shot(page: Page, name: string, isPhone: boolean) {
	if (!SHOTS) return;
	fs.mkdirSync(SHOTS, { recursive: true });
	await page.screenshot({ path: path.join(SHOTS, `${name}-${isPhone ? "phone" : "desktop"}.png`) });
}

async function showVisitorTab(page: Page, isPhone: boolean, tab: "Sources" | "Chat" | "Studio") {
	if (!isPhone) return;
	await page.getByRole("tab", { name: new RegExp(`^${tab}`) }).click();
}

test("share a notebook live, read it signed out, watch it update, expire and revoke it", async ({
	page,
	browser,
	request,
	token,
	workspace,
	notebook,
	isPhone,
}) => {
	test.setTimeout(180_000);
	const source = await seedPdfSource(request, workspace.id, notebook.id, "Asthma guideline");
	await seedOutput(request, workspace.id, notebook.id, "report", "ready", "Discharge teaching");
	await seedOutput(request, workspace.id, notebook.id, "quiz", "running", "Quiz in progress");

	// The Owner asks something; the fake answer cites the PDF's chunk.
	await openNotebook(page, workspace.id, notebook.id, "Chat");
	await send(page, `E2E_CITE:${source.chunk_id} what does the guideline say?`);
	await expect(
		page.getByRole("button", { name: `View cited chunk ${source.chunk_id}` })
	).toBeVisible({ timeout: 45_000 });

	// Share notebook… from the notebook menu.
	await page.getByRole("button", { name: "Notebook options" }).click();
	await page.getByRole("menuitem", { name: "Share notebook…" }).click();
	const dialog = page.getByTestId("notebook-share-dialog");
	await expect(
		dialog.getByText("Anyone with the link can view this notebook, including its sources.")
	).toBeVisible();
	await expect(dialog.getByText(/patient-identifiable/)).toHaveCount(0);
	await expect(dialog.getByRole("combobox")).toHaveText(/After 7 days/);
	await dialog.getByTestId("notebook-share-submit").click();
	const linkBox = dialog.getByTestId("notebook-share-url");
	await expect(linkBox).toHaveValue(/\/public\/notebook\//);
	const url = await linkBox.inputValue();
	await expect(dialog.getByText(/Shared read-only · expires/)).toBeVisible();
	await shot(page, "share-dialog", isPhone);
	await dialog.getByRole("button", { name: "Done" }).click();
	await expect(page.getByTestId("notebook-shared-badge")).toBeVisible();

	// A signed-out visitor.
	const { page: visitor, errors } = await visitorPage(browser, page);
	await visitor.goto(url);
	await expect(visitor.getByTestId("live-title")).toHaveText("E2E notebook", { timeout: 30_000 });
	await expect(visitor.getByTestId("shared-notice")).toContainText("Shared read-only");
	await expect(visitor.locator('[data-role="user"]').first()).toContainText("E2E_CITE");
	const citation = visitor.getByTestId("public-citation").first();
	await expect(citation).toHaveText("1");
	await expect(citation).toHaveAccessibleName("Asthma guideline.pdf, page 2");
	await expect(visitor.getByRole("textbox")).toHaveCount(0); // no chat input
	await shot(visitor, "public-notebook", isPhone);

	await showVisitorTab(visitor, isPhone, "Studio");
	const outputs = visitor.getByTestId("live-outputs");
	await expect(outputs.getByTestId("live-output")).toHaveCount(1);
	await expect(outputs).toContainText("Discharge teaching");
	await expect(outputs).not.toContainText("Quiz in progress");
	await outputs.getByRole("button", { name: /Discharge teaching/ }).click();
	const viewer = visitor.getByTestId("live-viewer");
	await expect(viewer.getByRole("heading", { name: "Discharge teaching" })).toBeVisible();
	await expect(viewer.getByTestId("live-artifact")).toBeVisible();
	await expect(viewer.getByTestId("live-download")).toHaveAttribute(
		"href",
		/\/api\/v1\/public-notebooks\/.+\/download$/
	);
	await visitor.waitForTimeout(1500);
	await shot(visitor, "output-open", isPhone);
	await visitor.keyboard.press("Escape");
	await expect(viewer).toHaveCount(0);

	// The cited source opens at its page, through the link only.
	await showVisitorTab(visitor, isPhone, "Chat");
	await visitor.getByTestId("public-citation").first().click();
	const sourceViewer = visitor.getByTestId("live-viewer");
	await expect(sourceViewer).toHaveAttribute("data-viewer", "source");
	await expect(sourceViewer.getByTestId("live-source-file")).toBeVisible();
	await expect(sourceViewer.locator("canvas").first()).toBeVisible({ timeout: 30_000 });
	await visitor.waitForTimeout(1500);
	await shot(visitor, "source-open", isPhone);
	await visitor.keyboard.press("Escape");

	// The source list shows the PDF, and the file itself answers with byte ranges.
	await showVisitorTab(visitor, isPhone, "Sources");
	await expect(visitor.getByTestId("live-sources")).toContainText("Asthma guideline.pdf");
	const shareToken = url.split("/public/notebook/")[1];
	const view = await visitor.request.get(
		`/api/v1/public-notebooks/${shareToken}/sources/${source.document_id}`
	);
	const fileUrl = ((await view.json()) as { file: { content_url: string } }).file.content_url;
	const ranged = await visitor.request.get(fileUrl, { headers: { Range: "bytes=0-4" } });
	expect(ranged.status()).toBe(206);
	expect(await ranged.text()).toBe("%PDF-");

	// A new output appears without sharing again or reloading.
	await showVisitorTab(visitor, isPhone, "Studio");
	await seedOutput(request, workspace.id, notebook.id, "mindmap", "ready", "Spacer mind map");
	await expect(outputs).toContainText("Spacer mind map", { timeout: 30_000 });
	await expect(outputs.getByTestId("live-output")).toHaveCount(2);

	// Expired: the page says so.
	const first = await linkState(request, token, workspace.id, notebook.id);
	expect(first?.share_token).toBe(shareToken);
	await expireLink(request, first?.id ?? 0);
	await visitor.reload();
	await expect(visitor.getByRole("heading", { name: "This link has expired" })).toBeVisible();

	// Sharing again makes a new address; revoking it from Public links ends it.
	await page.reload();
	await page.getByRole("button", { name: "Notebook options" }).click();
	await page.getByRole("menuitem", { name: "Share notebook…" }).click();
	await dialog.getByTestId("notebook-share-submit").click();
	await expect(linkBox).toHaveValue(/\/public\/notebook\//);
	const second = await linkBox.inputValue();
	expect(second).not.toBe(url);
	await dialog.getByRole("button", { name: "Done" }).click();

	await visitor.goto(second);
	await expect(visitor.getByTestId("live-title")).toHaveText("E2E notebook", { timeout: 30_000 });

	await page.goto(`/dashboard/${workspace.id}/workspace-settings/public-links`);
	const row = page.locator('[data-testid="public-link-row"][data-kind="notebook"]');
	await expect(row).toContainText("Live notebook");
	await expect(row).toContainText("E2E notebook");
	await row.getByRole("button", { name: "Revoke" }).click();
	await page.getByRole("alertdialog").getByRole("button", { name: "Revoke" }).click();
	await expect(row).toHaveCount(0);
	await visitor.reload();
	await expect(
		visitor.getByRole("heading", { name: "This link is no longer available" })
	).toBeVisible();

	// Stop sharing from the Shared badge.
	const created = await request.post(
		`${BACKEND_URL}/api/v1/workspaces/${workspace.id}/notebooks/${notebook.id}/public-link`,
		{ headers: authHeaders(token), data: { expires_in_days: 30 } }
	);
	expect(created.ok(), await created.text()).toBeTruthy();
	const third = ((await created.json()) as { share_token: string }).share_token;
	await openNotebook(page, workspace.id, notebook.id);
	await page.getByTestId("notebook-shared-badge").click();
	await page.getByTestId("notebook-shared-stop").click();
	await page.getByTestId("notebook-stop-sharing-confirm").click();
	await expect(page.getByTestId("notebook-shared-badge")).toHaveCount(0);
	await visitor.goto(`/public/notebook/${third}`);
	await expect(
		visitor.getByRole("heading", { name: "This link is no longer available" })
	).toBeVisible();

	expect(errors.filter((e) => !/ResizeObserver|AbortError/.test(e))).toEqual([]);
	await visitor.context().close();
});
