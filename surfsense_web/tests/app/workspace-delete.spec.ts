import type { Page } from "@playwright/test";
import { BACKEND_URL, mintTestToken } from "../helpers/api/auth";
import { createWorkspace, deleteWorkspace } from "../helpers/api/workspaces";
import { expect, test } from "./fixtures";

/**
 * Deleting a workspace from the account menu lands on the workspace opened
 * most recently, with no "You don't have access" toast for the deleted one;
 * and nobody can delete their only workspace.
 */

const MINT_SECRET = process.env.E2E_MINT_SECRET ?? "";
const SESSION_COOKIE_NAME = process.env.SESSION_COOKIE_NAME || "surfsense_session";

async function openWorkspaceMenu(page: Page) {
	await page.getByRole("button", { name: /^Account: / }).click();
	await page.getByRole("menuitem", { name: /^Switch workspace: / }).click();
}

function accessToasts(page: Page) {
	return page.locator("[data-sonner-toast]").filter({
		hasText: /don't have access to this workspace|FORBIDDEN|NOT_FOUND/,
	});
}

test("deleting the open workspace lands on the last one opened, with no error toast", async ({
	page,
	request,
	token,
	isPhone,
}) => {
	test.skip(isPhone, "the account menu's workspace submenu is the desktop control");
	const stamp = Date.now().toString(36);
	const keep = await createWorkspace(request, token, `keep-${stamp}`);
	const doomed = await createWorkspace(request, token, `doomed-${stamp}`);
	try {
		// Open "keep" first, so it is the most recent other workspace.
		await page.goto(`/dashboard/${keep.id}/notebooks`);
		await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
		await page.goto(`/dashboard/${doomed.id}/notebooks`);
		await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();

		await openWorkspaceMenu(page);
		await page.getByRole("menuitem", { name: "Delete workspace" }).click();
		const dialog = page.getByRole("alertdialog", { name: "Delete Workspace" });
		await expect(dialog).toBeVisible();
		await dialog.getByRole("button", { name: "Delete" }).click();

		await expect(page).toHaveURL(new RegExp(`/dashboard/${keep.id}/notebooks$`));
		await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();
		// Give stragglers for the deleted workspace time to fail, then look.
		await page.waitForTimeout(2_500);
		await expect(accessToasts(page)).toHaveCount(0);
		await expect(page).toHaveURL(new RegExp(`/dashboard/${keep.id}/notebooks$`));

		// It is gone from the switcher too.
		await openWorkspaceMenu(page);
		await expect(page.getByRole("menuitem", { name: doomed.name })).toHaveCount(0);
		await page.keyboard.press("Escape");
	} finally {
		await deleteWorkspace(request, token, doomed.id).catch(() => undefined);
		await deleteWorkspace(request, token, keep.id).catch(() => undefined);
	}
});

test("the only workspace cannot be deleted: an inline notice says why", async ({
	browser,
	baseURL,
	playwright,
	isPhone,
}) => {
	test.skip(isPhone, "the account menu's workspace submenu is the desktop control");
	const base = baseURL ?? "http://127.0.0.1:3940";
	const request = await playwright.request.newContext({
		storageState: { cookies: [], origins: [] },
	});
	// A fresh account owns exactly one workspace, its default.
	const email = `solo-${Date.now().toString(36)}@example.com`;
	const created = await request.post(`${BACKEND_URL}/__e2e__/accounts`, {
		data: { email },
		headers: { "Content-Type": "application/json", "X-E2E-Mint-Secret": MINT_SECRET },
	});
	expect(created.status(), await created.text()).toBe(201);
	const soloToken = await mintTestToken(request, email);
	const listed = await request.get(`${BACKEND_URL}/api/v1/workspaces`, {
		headers: { Authorization: `Bearer ${soloToken}` },
	});
	const spaces = (await listed.json()) as { id: number }[];
	expect(spaces).toHaveLength(1);
	const only = spaces[0].id;

	// The backend refuses on its own.
	const refused = await request.delete(`${BACKEND_URL}/api/v1/workspaces/${only}`, {
		headers: { Authorization: `Bearer ${soloToken}` },
	});
	expect(refused.status()).toBe(409);

	const context = await browser.newContext({ storageState: { cookies: [], origins: [] } });
	await context.addCookies([
		{ name: SESSION_COOKIE_NAME, value: soloToken, url: base, httpOnly: true, sameSite: "Lax" },
	]);
	const page = await context.newPage();
	page.on("dialog", async (native) => {
		await native.dismiss();
		throw new Error(`native ${native.type()} popup`);
	});
	await page.goto(`/dashboard/${only}/notebooks`);
	await expect(page.getByRole("heading", { name: "Notebooks", level: 1 })).toBeVisible();

	await openWorkspaceMenu(page);
	await page.getByRole("menuitem", { name: "Delete workspace" }).click();
	const notice = page.getByTestId("last-workspace-notice");
	await expect(notice).toBeVisible();
	await expect(notice).toContainText("is your only workspace, so it can't be deleted");
	await expect(page.getByRole("alertdialog", { name: "Delete Workspace" })).toHaveCount(0);
	await notice.getByRole("button", { name: "OK" }).click();
	await expect(notice).toBeHidden();
	await expect(page).toHaveURL(new RegExp(`/dashboard/${only}/notebooks$`));

	await context.close();
	await request.dispose();
});
