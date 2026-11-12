import type { APIRequestContext, Page } from "@playwright/test";
import { BACKEND_URL } from "../helpers/api/auth";
import { expect, test, uniqueName } from "./fixtures";

/**
 * Notifications: Delete from an item's menu, and Clear all on a tab after
 * asking, with the delete reaching another open tab through Zero. A running
 * job's item has no Delete and Clear all leaves it. The ⋯ menu shows on a
 * touch screen at any width. The tabs are All, Announcements and Unread.
 *
 * Every test of the run signs in as the same account, so each test tags its
 * rows' titles with a run-unique tag and only ever asserts on its own rows.
 * Clear all is the account's, though: it takes every row of the tab, other
 * tests' included. So the tests that use it run in the desktop project only,
 * one after another, and still assert only on their own rows.
 */

test.describe.configure({ mode: "serial" });

const SHOTS = process.env.E2E_SCREENSHOT_DIR;
const EMAIL = process.env.PLAYWRIGHT_TEST_EMAIL ?? "e2e-test@soloclef.net";
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

async function seed(request: APIRequestContext, rows: Seed[]) {
	const res = await request.post(`${BACKEND_URL}/__e2e__/notifications`, {
		data: { email: EMAIL, notifications: rows },
		// The request context carries the session cookie, so the backend's CSRF check wants a same-site Origin.
		headers: {
			"X-E2E-Mint-Secret": SECRET,
			Origin: process.env.PLAYWRIGHT_BASE_URL ?? BACKEND_URL,
		},
	});
	expect(res.status(), await res.text()).toBe(201);
}

/** The titles of this test's rows (those carrying its tag) still on the server. */
async function serverTitles(
	request: APIRequestContext,
	token: string,
	tag: string
): Promise<string[]> {
	const res = await request.get(`${BACKEND_URL}/api/v1/notifications`, {
		params: { limit: 100 },
		headers: { Authorization: `Bearer ${token}` },
	});
	expect(res.ok()).toBeTruthy();
	const body = (await res.json()) as { items: { title: string }[] };
	return body.items
		.map((i) => i.title)
		.filter((title) => title.includes(tag))
		.sort();
}

/** The panel: a popover on a desktop, a drawer on a phone; either way the dialog with the tabs. */
const panel = (page: Page) =>
	page.getByRole("dialog").filter({ has: page.getByRole("button", { name: /^Unread\b/ }) });

async function openNotifications(page: Page, isPhone: boolean) {
	await page.goto("/dashboard/notebooks");
	if (isPhone) {
		await page.getByRole("button", { name: "Open menu" }).click();
	}
	await page.getByRole("button", { name: "Notifications", exact: true }).click();
	await expect(panel(page)).toBeVisible();
}

const tab = (page: Page, label: string) =>
	panel(page).getByRole("button", { name: new RegExp(`^${label}\\b`) });

/** This test's rows: two status items (one read) and an announcement, all tagged. */
function rows(tag: string) {
	const titles = {
		ward: `Ward notes are ready ${tag}`,
		discharge: `Discharge letter is ready ${tag}`,
		podcasts: `Podcasts up to 30 minutes ${tag}`,
	};
	const seeds: Seed[] = [
		{ type: "document_processing", title: titles.ward, minutes_ago: 1 },
		{ type: "document_processing", title: titles.discharge, read: true, minutes_ago: 2 },
		{
			type: "site_announcement",
			title: titles.podcasts,
			message: "Try a **long** one.",
			metadata: { announcement_id: 0 },
			minutes_ago: 3,
		},
	];
	return { titles, seeds };
}

test("Delete from an item's menu, also gone from another open tab", async ({
	page,
	context,
	request,
	token,
	isPhone,
}) => {
	const tag = uniqueName("n").slice(2);
	const { titles, seeds } = rows(tag);
	await seed(request, seeds);
	await openNotifications(page, isPhone);
	await expect(panel(page).getByText(titles.ward)).toBeVisible();

	const other = await context.newPage();
	await openNotifications(other, isPhone);
	await expect(panel(other).getByText(titles.ward)).toBeVisible();

	const actions = panel(page).getByRole("button", { name: `Actions for ${titles.ward}` });
	if (isPhone) {
		// Touch: no hover to bring it up, so always shown.
		await expect(actions).toHaveCSS("opacity", "1");
	} else {
		// A mouse: shown on the item's hover.
		await expect(actions).toHaveCSS("opacity", "0");
		await panel(page).getByText(titles.ward).hover();
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

	await expect(panel(page).getByText(titles.ward)).toHaveCount(0);
	await expect(panel(page).getByText(titles.discharge)).toBeVisible();
	// The other tab drops it with no reload: Zero carried the delete.
	await expect(panel(other).getByText(titles.ward)).toHaveCount(0);
	await expect
		.poll(() => serverTitles(request, token, tag))
		.toEqual([titles.discharge, titles.podcasts].sort());
	await other.close();
});

test("Clear all on a tab asks first and clears only that tab", async ({
	page,
	context,
	request,
	token,
	isPhone,
}) => {
	test.skip(isPhone, "Clear all takes the whole account's tab: one project runs it");
	const tag = uniqueName("n").slice(2);
	const { titles, seeds } = rows(tag);
	await seed(request, seeds);
	await openNotifications(page, isPhone);
	await expect(panel(page).getByText(titles.podcasts)).toBeVisible();

	const other = await context.newPage();
	await openNotifications(other, isPhone);
	await expect(panel(other).getByText(titles.podcasts)).toBeVisible();

	// There is no Mentions tab: comments, and so mentions, are gone.
	await expect(tab(page, "Mentions")).toHaveCount(0);

	await tab(page, "Announcements").click();
	await panel(page).getByRole("button", { name: "Clear all" }).click();
	const ask = panel(page).getByRole("group", { name: "Clear all announcements?" });
	await expect(ask).toBeVisible();
	if (SHOTS)
		await page.screenshot({
			path: `${SHOTS}/web-${isPhone ? "phone" : "desktop"}-02-clear-confirm.png`,
		});

	// Cancel leaves everything.
	await ask.getByRole("button", { name: "Cancel" }).click();
	await expect(panel(page).getByText(titles.podcasts)).toBeVisible();

	await panel(page).getByRole("button", { name: "Clear all" }).click();
	await panel(page)
		.getByRole("group", { name: "Clear all announcements?" })
		.getByRole("button", { name: "Clear all" })
		.click();
	await expect(panel(page).getByText(titles.podcasts)).toHaveCount(0);

	await tab(page, "All").click();
	await expect(panel(page).getByText(titles.ward)).toBeVisible();
	await expect(panel(page).getByText(titles.discharge)).toBeVisible();
	await expect(panel(other).getByText(titles.podcasts)).toHaveCount(0);
	if (SHOTS)
		await page.screenshot({
			path: `${SHOTS}/web-${isPhone ? "phone" : "desktop"}-03-after-clear-announcements.png`,
		});

	// Unread clears only the unread ones: the read status row stays.
	await tab(page, "Unread").click();
	await panel(page).getByRole("button", { name: "Clear all" }).click();
	await panel(page)
		.getByRole("group", { name: "Clear all unread notifications?" })
		.getByRole("button", { name: "Clear all" })
		.click();
	await expect(panel(page).getByText(titles.ward)).toHaveCount(0);
	await tab(page, "All").click();
	await expect(panel(page).getByText(titles.discharge)).toBeVisible();

	await expect.poll(() => serverTitles(request, token, tag)).toEqual([titles.discharge]);
	await other.close();
});

test("a running job's item has no Delete, and Clear all leaves it", async ({
	page,
	request,
	token,
	isPhone,
}) => {
	test.skip(isPhone, "Clear all takes the whole account's tab: one project runs it");
	const tag = uniqueName("n").slice(2);
	const running = `Lab results are being read ${tag}`;
	const ready = `Ward notes are ready ${tag}`;
	await seed(request, [
		{ type: "document_processing", title: running, running: true, minutes_ago: 1 },
		{
			type: "document_processing",
			title: ready,
			metadata: { status: "completed" },
			minutes_ago: 2,
		},
	]);
	await openNotifications(page, isPhone);
	await expect(panel(page).getByText(running)).toBeVisible();
	await expect(panel(page).getByRole("button", { name: `Actions for ${ready}` })).toHaveCount(1);
	await expect(panel(page).getByRole("button", { name: `Actions for ${running}` })).toHaveCount(0);

	await panel(page).getByRole("button", { name: "Clear all" }).click();
	await panel(page)
		.getByRole("group", { name: "Clear all notifications?" })
		.getByRole("button", { name: "Clear all" })
		.click();
	await expect(panel(page).getByText(ready)).toHaveCount(0);
	await expect(panel(page).getByText(running)).toBeVisible();
	if (SHOTS)
		await page.screenshot({
			path: `${SHOTS}/web-${isPhone ? "phone" : "desktop"}-04-running-kept.png`,
		});
	await expect.poll(() => serverTitles(request, token, tag)).toEqual([running]);
});

test.describe("on a touch screen as wide as an iPad", () => {
	test.use({ viewport: { width: 1024, height: 1366 }, hasTouch: true, isMobile: true });

	test("each item's ⋯ shows with no hover", async ({ page, request }, testInfo) => {
		test.skip(testInfo.project.name !== "desktop", "one tablet run is enough");
		const title = `Ward notes are ready ${uniqueName("n").slice(2)}`;
		await seed(request, [{ type: "document_processing", title }]);
		await openNotifications(page, false);
		await expect(panel(page).getByRole("button", { name: `Actions for ${title}` })).toHaveCSS(
			"opacity",
			"1"
		);
		if (SHOTS) await page.screenshot({ path: `${SHOTS}/web-tablet-01-item-menu-shown.png` });
	});
});
