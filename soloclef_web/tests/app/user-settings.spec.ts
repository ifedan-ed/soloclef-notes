import type { Page } from "@playwright/test";
import { expectNoOverflow } from "../helpers/layout/overflow";
import { expect, test } from "./fixtures";

/** User settings: /dashboard/<ws>/user-settings/<tab>. */

const TABS: [slug: string, label: string][] = [
	["profile", "Profile"],
	["appearance", "Appearance"],
	["billing", "Billing"],
	["prompts", "My Prompts"],
];

/** Kept in the code, out of sight: for administrators only. */
const HIDDEN: [slug: string, label: string][] = [
	["api-key", "API Access"],
	["community-prompts", "Community Prompts"],
	["agent-permissions", "Agent Permissions"],
];

const settingsUrl = (ws: number, slug: string) => `/dashboard/${ws}/user-settings/${slug}`;

async function openTab(page: Page, ws: number, slug: string) {
	await page.goto(settingsUrl(ws, slug));
	await expect(page.getByRole("heading", { name: "User Settings", level: 1 })).toBeVisible();
}

test.describe("User settings", () => {
	test("/user-settings opens Profile; every tab renders and fits", async ({ page, workspace }) => {
		await page.goto(`/dashboard/${workspace.id}/user-settings`);
		await expect(page).toHaveURL(/\/user-settings\/profile$/);
		for (const [slug, label] of TABS) {
			const link = page.getByRole("link", { name: label, exact: true }).filter({ visible: true });
			await link.first().click();
			await expect(page).toHaveURL(new RegExp(`/user-settings/${slug}$`));
			await expectNoOverflow(page, `user settings: ${label}`);
		}
	});

	test("profile: change the display name", async ({ page, workspace, request, token }) => {
		await openTab(page, workspace.id, "profile");
		const name = page.getByLabel("Display Name");
		const original = await name.inputValue();
		// Unique per run: the desktop and phone projects share this user and may
		// run this test at the same moment.
		const fresh = `E2E Tester ${Date.now().toString(36)}`;
		await name.fill(fresh);
		await page.getByRole("button", { name: "Save Changes" }).click();
		await expect
			.poll(async () => {
				const r = await request.get("/users/me", { headers: { Authorization: `Bearer ${token}` } });
				return ((await r.json()) as { display_name: string | null }).display_name;
			})
			.toBe(fresh);
		// Put it back; other specs read the user menu name.
		await name.fill(original);
		await page.getByRole("button", { name: "Save Changes" }).click();
	});

	test("profile: the delete-account dialog needs DELETE typed (not confirmed)", async ({
		page,
		workspace,
	}) => {
		await openTab(page, workspace.id, "profile");
		await page.getByRole("button", { name: "Delete account" }).click();
		const dialog = page.getByRole("dialog", { name: "Delete your account?" });
		await expect(dialog).toBeVisible();
		await expectNoOverflow(page, "delete account dialog");
		const confirm = dialog.getByRole("button", { name: "Delete my account" });
		await expect(confirm).toBeDisabled();
		await dialog.getByLabel("Type DELETE to confirm").fill("DELETE");
		await expect(confirm).toBeEnabled();
		await dialog.getByRole("button", { name: "Cancel" }).click();
		await expect(dialog).toBeHidden();
	});

	test("hidden sections are not in the nav and their addresses go to Profile", async ({
		page,
		workspace,
	}) => {
		await openTab(page, workspace.id, "profile");
		for (const [, label] of HIDDEN) {
			await expect(page.getByRole("link", { name: label, exact: true })).toHaveCount(0);
		}
		for (const [slug] of HIDDEN) {
			await page.goto(settingsUrl(workspace.id, slug));
			await expect(page).toHaveURL(/\/user-settings\/profile$/);
		}
	});

	test("my prompts: no sharing with the community for a member", async ({ page, workspace }) => {
		await openTab(page, workspace.id, "prompts");
		await page.getByRole("button", { name: "New", exact: true }).click();
		const dialog = page.getByRole("dialog", { name: "New prompt" });
		await expect(dialog).toBeVisible();
		await expect(dialog.getByText("Share with community")).toHaveCount(0);
		await page.keyboard.press("Escape");
	});

	test("my prompts: create, edit, delete", async ({ page, workspace }) => {
		await openTab(page, workspace.id, "prompts");
		const name = `E2E settings prompt ${Date.now()}`;
		await page.getByRole("button", { name: "New", exact: true }).click();
		const dialog = page.getByRole("dialog", { name: "New prompt" });
		await expect(dialog).toBeVisible();
		await expectNoOverflow(page, "new prompt dialog");
		await dialog.getByLabel("Name").fill(name);
		await dialog.getByLabel("Prompt template").fill("Explain {selection} simply.");
		await dialog.getByRole("button", { name: "Create" }).click();
		await expect(dialog).toBeHidden();
		await expect(page.getByText(name)).toBeVisible();

		const actions = async () => {
			const card = page.locator(".group").filter({ hasText: name }).last();
			await card.hover();
			await card.getByRole("button", { name: "Prompt actions" }).click();
		};
		await actions();
		await page.getByRole("menuitem", { name: "Edit" }).click();
		const edit = page.getByRole("dialog", { name: "Edit prompt" });
		await edit.getByLabel("Name").fill(`${name} v2`);
		await edit.getByRole("button", { name: "Update" }).click();
		await expect(page.getByText(`${name} v2`)).toBeVisible();

		await actions();
		await page.getByRole("menuitem", { name: "Delete" }).click();
		const alert = page.getByRole("alertdialog", { name: "Delete prompt" });
		await alert.getByRole("button", { name: "Delete" }).click();
		await expect(page.getByText(`${name} v2`)).toBeHidden();
	});

	test("appearance: timestamps toggle is local", async ({ page, workspace }) => {
		await openTab(page, workspace.id, "appearance");
		const toggle = page.getByRole("switch", { name: "Show message timestamps" });
		const before = await toggle.getAttribute("aria-checked");
		await toggle.click();
		await expect(toggle).not.toHaveAttribute("aria-checked", before ?? "");
	});
});
