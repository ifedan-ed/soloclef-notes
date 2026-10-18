import type { APIRequestContext, Page } from "@playwright/test";
import { BACKEND_URL } from "../helpers/api/auth";
import { expect, test } from "./fixtures";

/**
 * Notifications: Delete from an item's menu, and Clear all on a tab after
 * asking, with the delete reaching another open tab through Zero. A running
 * job's item has no Delete and Clear all leaves it. The ⋯ menu shows on a
 * touch screen at any width.
 *
 * Every seeded row belongs to the test's own workspace, so parallel tests of
 * the same e2e account never see (or clear) each other's.
 */

const SHOTS = process.env.E2E_SCREENSHOT_DIR;
const EMAIL = process.env.PLAYWRIGHT_TEST_EMAIL ?? "e2e-test@surfsense.net";
const SECRET = process.env.E2E_MINT_SECRET ?? "e2e-mint-secret-not-for-production";

type Seed = {
	type: string;
	title: string;
	message?: string;
	read?: boolean;
	metadata?: Record<string, unknown>;
	minutes_ago?: number;
	/** A job still running (status in_progress, its heartbeat set by the seed route). */
	running?: boolean;
};

async function seed(request: APIRequestContext, workspaceId: number, rows: Seed[]) {
	const res = await request.post(`${BACKEND_URL}/__e2e__/notifications`, {
		data: { email: EMAIL, notifications: rows.map((r) => ({ ...r, workspace_id: workspaceId })) },
		// The request context carries the session cookie, so the backend's CSRF check wants a same-site Origin.
		headers: {
			"X-E2E-Mint-Secret": SECRET,
			Origin: process.env.PLAYWRIGHT_BASE_URL ?? BACKEND_URL,
		},
	});
	expect(res.status(), await res.text()).toBe(201);
}

async function serverTitles(
	request: APIRequestContext,
	token: string,
	workspaceId: number
): Promise<string[]> {
	const res = await request.get(`${BACKEND_URL}/api/v1/notifications`, {
		params: { workspace_id: workspaceId },
		headers: { Authorization: `Bearer ${token}` },
	});
	expect(res.ok()).toBeTruthy();
	const body = (await res.json()) as { items: { title: string; workspace_id: number | null }[] };
	return body.items
		.filter((i) => i.workspace_id === workspaceId)
		.map((i) => i.title)
		.sort();
}

/** The panel: a popover on a desktop, a drawer on a phone; either way the dialog with the tabs. */
const panel = (page: Page) =>
	page.getByRole("dialog").filter({ has: page.getByRole("button", { name: /^Unread\b/ }) });

async function openNotifications(page: Page, workspaceId: number, isPhone: boolean) {
	await page.goto(`/dashboard/${workspaceId}/notebooks`);
	if (isPhone) {
		await page.getByRole("button", { name: "Open menu" }).click();
	}
	await page.getByRole("button", { name: "Notifications", exact: true }).click();
	await expect(panel(page)).toBeVisible();
}

const tab = (page: Page, label: string) =>
	panel(page).getByRole("button", { name: new RegExp(`^${label}\\b`) });

const ROWS: Seed[] = [
	{ type: "new_mention", title: "Ana mentioned you", minutes_ago: 1 },
	{ type: "comment_reply", title: "Ben replied", read: true, minutes_ago: 2 },
	{ type: "document_processing", title: "Ward notes are ready", minutes_ago: 3 },
	{ type: "document_processing", title: "Discharge letter is ready", read: true, minutes_ago: 4 },
	{
		type: "site_announcement",
		title: "Podcasts up to 30 minutes",
		message: "Try a **long** one.",
		metadata: { announcement_id: 0 },
		minutes_ago: 5,
	},
];

test("Delete from an item's menu, also gone from another open tab", async ({
	page,
	context,
	request,
	token,
	workspace,
	isPhone,
}) => {
	await seed(request, workspace.id, ROWS);
	await openNotifications(page, workspace.id, isPhone);
	await expect(panel(page).getByText("Ward notes are ready")).toBeVisible();

	const other = await context.newPage();
	await openNotifications(other, workspace.id, isPhone);
	await expect(panel(other).getByText("Ward notes are ready")).toBeVisible();

	const actions = panel(page).getByRole("button", { name: "Actions for Ward notes are ready" });
	if (isPhone) {
		// Touch: no hover to bring it up, so always shown.
		await expect(actions).toHaveCSS("opacity", "1");
	} else {
		// A mouse: shown on the item's hover.
		await expect(actions).toHaveCSS("opacity", "0");
		await panel(page).getByText("Ward notes are ready").hover();
		await expect(actions).toHaveCSS("opacity", "1");
	}
	await actions.click();
	const del = page.getByRole("menuitem", { name: "Delete" });
	await expect(del).toBeVisible();
	if (SHOTS)
		await page.screenshot({
			path: `${SHOTS}/web-${isPhone ? "phone" : "desktop"}-01-item-menu.png`,
		});
	await del.click();

	await expect(panel(page).getByText("Ward notes are ready")).toHaveCount(0);
	await expect(panel(page).getByText("Discharge letter is ready")).toBeVisible();
	// The other tab drops it with no reload: Zero carried the delete.
	await expect(panel(other).getByText("Ward notes are ready")).toHaveCount(0);
	await expect(tab(page, "All")).toContainText("4");
	await expect
		.poll(() => serverTitles(request, token, workspace.id))
		.not.toContain("Ward notes are ready");
	await other.close();
});

test("Clear all on a tab asks first and clears only that tab", async ({
	page,
	context,
	request,
	token,
	workspace,
	isPhone,
}) => {
	await seed(request, workspace.id, ROWS);
	await openNotifications(page, workspace.id, isPhone);
	await expect(panel(page).getByText("Ana mentioned you")).toBeVisible();

	const other = await context.newPage();
	await openNotifications(other, workspace.id, isPhone);
	await expect(panel(other).getByText("Ben replied")).toBeVisible();

	await tab(page, "Mentions").click();
	await panel(page).getByRole("button", { name: "Clear all" }).click();
	const ask = panel(page).getByRole("group", { name: "Clear all mentions?" });
	await expect(ask).toBeVisible();
	if (SHOTS)
		await page.screenshot({
			path: `${SHOTS}/web-${isPhone ? "phone" : "desktop"}-02-clear-confirm.png`,
		});

	// Cancel leaves everything.
	await ask.getByRole("button", { name: "Cancel" }).click();
	await expect(panel(page).getByText("Ana mentioned you")).toBeVisible();

	await panel(page).getByRole("button", { name: "Clear all" }).click();
	await panel(page)
		.getByRole("group", { name: "Clear all mentions?" })
		.getByRole("button", { name: "Clear all" })
		.click();
	await expect(panel(page).getByText("No mentions")).toBeVisible();

	await tab(page, "All").click();
	await expect(panel(page).getByText("Ward notes are ready")).toBeVisible();
	await expect(panel(page).getByText("Podcasts up to 30 minutes")).toBeVisible();
	await expect(panel(page).getByText("Ben replied")).toHaveCount(0);
	await expect(panel(other).getByText("Ben replied")).toHaveCount(0);
	await expect(panel(other).getByText("Ana mentioned you")).toHaveCount(0);
	if (SHOTS)
		await page.screenshot({
			path: `${SHOTS}/web-${isPhone ? "phone" : "desktop"}-03-after-clear-mentions.png`,
		});

	// Unread clears only the unread ones: the read status row stays.
	await tab(page, "Unread").click();
	await panel(page).getByRole("button", { name: "Clear all" }).click();
	await panel(page)
		.getByRole("group", { name: "Clear all unread notifications?" })
		.getByRole("button", { name: "Clear all" })
		.click();
	await expect(panel(page).getByText("No unread notifications")).toBeVisible();
	await tab(page, "All").click();
	await expect(panel(page).getByText("Discharge letter is ready")).toBeVisible();

	await expect
		.poll(() => serverTitles(request, token, workspace.id))
		.toEqual(["Discharge letter is ready"]);
	await other.close();
});

test("a running job's item has no Delete, and Clear all leaves it", async ({
	page,
	request,
	token,
	workspace,
	isPhone,
}) => {
	await seed(request, workspace.id, [
		{
			type: "document_processing",
			title: "Lab results are being read",
			running: true,
			minutes_ago: 1,
		},
		{
			type: "document_processing",
			title: "Ward notes are ready",
			metadata: { status: "completed" },
			minutes_ago: 2,
		},
		{ type: "new_mention", title: "Ana mentioned you", minutes_ago: 3 },
	]);
	await openNotifications(page, workspace.id, isPhone);
	await expect(panel(page).getByText("Lab results are being read")).toBeVisible();
	await expect(
		panel(page).getByRole("button", { name: "Actions for Ward notes are ready" })
	).toHaveCount(1);
	await expect(
		panel(page).getByRole("button", { name: "Actions for Lab results are being read" })
	).toHaveCount(0);

	await panel(page).getByRole("button", { name: "Clear all" }).click();
	await panel(page)
		.getByRole("group", { name: "Clear all notifications?" })
		.getByRole("button", { name: "Clear all" })
		.click();
	await expect(panel(page).getByText("Ward notes are ready")).toHaveCount(0);
	await expect(panel(page).getByText("Ana mentioned you")).toHaveCount(0);
	await expect(panel(page).getByText("Lab results are being read")).toBeVisible();
	await expect(tab(page, "All")).toContainText("1");
	// Only a running item left: nothing for Clear all to take.
	await expect(panel(page).getByRole("button", { name: "Clear all" })).toBeDisabled();
	if (SHOTS)
		await page.screenshot({
			path: `${SHOTS}/web-${isPhone ? "phone" : "desktop"}-04-running-kept.png`,
		});
	await expect
		.poll(() => serverTitles(request, token, workspace.id))
		.toEqual(["Lab results are being read"]);
});

test.describe("on a touch screen as wide as an iPad", () => {
	test.use({ viewport: { width: 1024, height: 1366 }, hasTouch: true, isMobile: true });

	test("each item's ⋯ shows with no hover", async ({ page, request, workspace }, testInfo) => {
		test.skip(testInfo.project.name !== "desktop", "one tablet run is enough");
		await seed(request, workspace.id, [{ type: "new_mention", title: "Ana mentioned you" }]);
		await openNotifications(page, workspace.id, false);
		await expect(
			panel(page).getByRole("button", { name: "Actions for Ana mentioned you" })
		).toHaveCSS("opacity", "1");
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/web-tablet-01-item-menu-shown.png` });
	});
});
